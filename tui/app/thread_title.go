package app

import (
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// Rust parity: codex-rs/tui/src/app/thread_title.rs (#40495).

const (
	// ThreadTitleMaxChars bounds generated titles to the display limit.
	ThreadTitleMaxChars = 36
	// ThreadTitlePromptMaxBytes bounds the temporary structured title request.
	ThreadTitlePromptMaxBytes = 960
	threadTitleRecentMessages = 8
)

// ThreadTitleMessage is one substantive conversation message used for a
// /rename title suggestion.
type ThreadTitleMessage struct {
	Role  string
	Text  string
	Phase string
}

type generatedThreadTitle struct {
	Title string `json:"title"`
}

// ThreadTitleOutputSchema constrains the temporary structured title turn.
func ThreadTitleOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{
				"type":      "string",
				"minLength": 1,
				"maxLength": ThreadTitleMaxChars,
			},
		},
		"required":             []string{"title"},
		"additionalProperties": false,
	}
}

// ThreadTitleInstructions is the shared title-generation instruction text.
func ThreadTitleInstructions() string {
	return "Generate a concise, single-line task title of at most " +
		"36 characters and under five words where possible. " +
		"Start with an imperative verb. Capitalize only the first word unless the " +
		"user's language, proper nouns, acronyms, or code terms require otherwise. " +
		"Preserve ticket references exactly. Write in the user's language. " +
		"Do not use quotes, markdown, or trailing punctuation. " +
		"Do not answer the request."
}

// ThreadTitlePrompt builds a bounded request without splitting a Unicode
// character.
func ThreadTitlePrompt(userMessage string) string {
	prefix := ThreadTitleInstructions() + "\n\nUser prompt:\n"
	remaining := ThreadTitlePromptMaxBytes - len(prefix)
	if remaining < 0 {
		remaining = 0
	}
	var builder strings.Builder
	used := 0
	for _, r := range strings.TrimSpace(userMessage) {
		size := utf8.RuneLen(r)
		if used+size > remaining {
			break
		}
		builder.WriteRune(r)
		used += size
	}
	return prefix + builder.String()
}

// RecentThreadTitleConversation selects the latest substantive user and
// assistant messages, escapes their markup, and bounds the whole conversation
// to the title prompt budget.
func RecentThreadTitleConversation(messages []ThreadTitleMessage) (string, bool) {
	selected := make([]ThreadTitleMessage, 0, threadTitleRecentMessages)
	for index := len(messages) - 1; index >= 0 && len(selected) < threadTitleRecentMessages; index-- {
		message := messages[index]
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(message.Role)) {
		case "user":
			selected = append(selected, ThreadTitleMessage{Role: "user", Text: text})
		case "assistant":
			// Rust omits commentary: only substantive assistant answers shape
			// the suggested title.
			if strings.EqualFold(strings.TrimSpace(message.Phase), "commentary") {
				continue
			}
			selected = append(selected, ThreadTitleMessage{Role: "assistant", Text: text})
		}
	}
	if len(selected) == 0 {
		return "", false
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	for index := range selected {
		selected[index].Text = escapeThreadTitleMarkup(selected[index].Text)
	}

	emptyPromptBytes := len(ThreadTitlePrompt(""))
	conversationBytes := ThreadTitlePromptMaxBytes - emptyPromptBytes
	if conversationBytes < 0 {
		conversationBytes = 0
	}
	markupBytes := len("<conversation>\n") + len("\n</conversation>") + len(selected) - 1
	for _, message := range selected {
		markupBytes += len("<message role=\"\"></message>") + len(message.Role)
	}
	availableText := conversationBytes - markupBytes
	if availableText < 0 {
		availableText = 0
	}
	messageBytes := availableText / len(selected)
	contentBytes := 0
	for _, message := range selected {
		contentBytes += len(message.Text)
	}
	shouldTruncate := contentBytes > availableText
	lines := make([]string, 0, len(selected))
	for _, message := range selected {
		text := message.Text
		if shouldTruncate && len(text) > messageBytes {
			text = truncateThreadTitleText(text, messageBytes)
		}
		lines = append(lines, "<message role=\""+message.Role+"\">"+text+"</message>")
	}
	return "<conversation>\n" + strings.Join(lines, "\n") + "\n</conversation>", true
}

func escapeThreadTitleMarkup(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	return strings.ReplaceAll(text, ">", "&gt;")
}

func truncateThreadTitleText(text string, limit int) string {
	if limit < 0 {
		limit = 0
	}
	if len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	text = text[:end]
	if entityStart := strings.LastIndexByte(text, '&'); entityStart >= 0 &&
		!strings.Contains(text[entityStart:], ";") {
		text = text[:entityStart]
	}
	return text
}

// RecentConversationThreadTitlePrompt bounds the whole suggestion prompt while
// preserving complete Unicode characters.
func RecentConversationThreadTitlePrompt(conversation string) string {
	prefix := ThreadTitleInstructions() + "\n" +
		"Prioritize the current task and latest substantive user request.\n\n" +
		"Recent conversation messages:\n"
	conversation = strings.TrimSpace(conversation)
	remaining := ThreadTitlePromptMaxBytes - len(prefix)
	if remaining < 0 {
		remaining = 0
	}
	start := len(conversation) - remaining
	if start < 0 {
		start = 0
	}
	for start < len(conversation) && !utf8.RuneStart(conversation[start]) {
		start++
	}
	return prefix + conversation[start:]
}

// ParseGeneratedThreadTitle normalizes the model's structured title response,
// removing presentation-only quotes and trailing punctuation while preserving
// meaningful leading punctuation such as ".NET" and "!important".
func ParseGeneratedThreadTitle(response string) (string, bool) {
	if !strings.HasPrefix(strings.TrimSpace(response), "{") {
		return "", false
	}
	decoder := json.NewDecoder(strings.NewReader(response))
	decoder.DisallowUnknownFields()
	var generated generatedThreadTitle
	if err := decoder.Decode(&generated); err != nil {
		return "", false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", false
	}
	title := strings.TrimSpace(generated.Title)
	title = strings.Trim(title, "\"'`\u201c\u201d\u2018\u2019")
	title = strings.Join(strings.Fields(title), " ")
	title = strings.TrimSpace(strings.TrimRight(title, ".?!"))
	if title == "" {
		return "", false
	}
	if utf8.RuneCountInString(title) > ThreadTitleMaxChars {
		title = string([]rune(title)[:ThreadTitleMaxChars])
	}
	return title, true
}
