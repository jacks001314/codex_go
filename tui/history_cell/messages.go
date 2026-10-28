package historycell

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"codex_go/tui"
	"codex_go/utils"
)

// Rust parity: codex-rs/tui/src/history_cell/messages.rs.

const (
	ansiUserMessagePrefix    = "\x1b[1;2m"
	ansiUserMessageSpoken    = "\x1b[1;31m"
	ansiUserMessageSpokenEnd = "\x1b[22;39m"
	ansiUserMessagePrefixEnd = "\x1b[22m"
	ansiUserMessageReset     = "\x1b[0m"
	ansiUserMessageMention   = "\x1b[36m"
)

var userMessageMentionRE = regexp.MustCompile(`[$@][A-Za-z0-9][A-Za-z0-9_-]*`)

// SanitizeUserText mirrors Rust `sanitize_user_text`: it removes CSI escape
// sequences and control characters from user text while preserving tabs and
// newlines. An unterminated CSI sequence drops the remainder of the text,
// matching Rust's span split.
func SanitizeUserText(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	for index := 0; index < len(text); {
		r, size := utf8.DecodeRuneInString(text[index:])
		if r == utf8.RuneError && size <= 1 {
			// Not reachable for valid UTF-8 input; keep the byte so the loop advances.
			out.WriteByte(text[index])
			index++
			continue
		}
		if r == 0x1b {
			rest := text[index+size:]
			if strings.HasPrefix(rest, "[") {
				if end := csiSequenceEnd(rest); end >= 0 {
					index += size + end
					continue
				}
				return out.String()
			}
			index += size
			continue
		}
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			index += size
			continue
		}
		out.WriteString(text[index : index+size])
		index += size
	}
	return out.String()
}

// csiSequenceEnd returns the length of the CSI sequence in rest, counting the
// leading "[", up to and including its final byte ('@'..'~'). It returns -1
// when the sequence never terminates.
func csiSequenceEnd(rest string) int {
	for offset := 1; offset < len(rest); offset++ {
		if final := rest[offset]; final >= '@' && final <= '~' {
			return offset + 1
		}
	}
	return -1
}

type TextElement struct {
	Start int
	End   int
}

type UserHistoryCell struct {
	Message         string
	TextElements    []TextElement
	LocalImagePaths []string
	RemoteImageURLs []string
	Presentation    UserMessagePresentation
}

// UserMessagePresentation mirrors Rust #57291's explicit enum: typed prompts
// render with the dim marker, while speech transcripts keep their own red
// marker and leading-whitespace handling.
type UserMessagePresentation int

const (
	UserMessagePresentationPlain UserMessagePresentation = iota
	UserMessagePresentationSpoken
)

// IsSpoken reports whether the cell renders a speech transcript.
func (c UserHistoryCell) IsSpoken() bool {
	return c.Presentation == UserMessagePresentationSpoken
}

func NewUserPrompt(message string, textElements []TextElement, localImagePaths []string, remoteImageURLs []string) UserHistoryCell {
	return UserHistoryCell{
		Message:         message,
		TextElements:    append([]TextElement(nil), textElements...),
		LocalImagePaths: append([]string(nil), localImagePaths...),
		RemoteImageURLs: append([]string(nil), remoteImageURLs...),
		Presentation:    UserMessagePresentationPlain,
	}
}

// NewSpokenUserPrompt builds a speech-transcript cell (Rust
// new_spoken_user_prompt).
func NewSpokenUserPrompt(message string) UserHistoryCell {
	cell := NewUserPrompt(message, nil, nil, nil)
	cell.Presentation = UserMessagePresentationSpoken
	return cell
}

func (c UserHistoryCell) DisplayLines(width int) []string {
	wrapWidth := width - tui.LivePrefixCols - 1
	if wrapWidth < 1 {
		wrapWidth = 1
	}
	wrappedRemoteImages := []string{}
	for index := range c.RemoteImageURLs {
		wrappedRemoteImages = append(wrappedRemoteImages, tui.AdaptiveWrapLine(localImageLabelText(index+1), tui.WrapOptions{
			Width:      wrapWidth,
			BreakWords: true,
		})...)
	}
	message := strings.TrimRight(c.Message, "\r\n")
	// Speech transcripts can begin with whitespace; the marker supplies its own
	// separator (Rust #57291).
	if c.IsSpoken() {
		message = strings.TrimLeft(message, " \t\n\r")
	}
	wrappedMessage := []string{}
	if message != "" {
		for _, line := range strings.Split(message, "\n") {
			wrappedMessage = append(wrappedMessage, tui.WrapLine(line, tui.WrapOptions{
				Width:      wrapWidth,
				BreakWords: true,
			})...)
		}
		wrappedMessage = annotateUserMessageURLs(wrappedMessage, message)
		wrappedMessage = trimTrailingBlankLines(wrappedMessage)
	}
	if len(wrappedRemoteImages) == 0 && len(wrappedMessage) == 0 {
		return nil
	}
	lines := []string{styleUserMessageLine("", width)}
	for _, line := range wrappedRemoteImages {
		lines = append(lines, styleUserMessageLine("  "+line, width))
	}
	if len(wrappedRemoteImages) > 0 && len(wrappedMessage) > 0 {
		lines = append(lines, styleUserMessageLine("", width))
	}
	for index, line := range wrappedMessage {
		prefix := "  "
		if index == 0 {
			if c.IsSpoken() {
				prefix = ansiUserMessageSpoken + "\u203a " + ansiUserMessageSpokenEnd
			} else {
				prefix = ansiUserMessagePrefix + "\u203a " + ansiUserMessagePrefixEnd
			}
		}
		lines = append(lines, styleUserMessageLine(prefix+styleUserMessageMentions(line), width))
	}
	lines = append(lines, styleUserMessageLine("", width))
	return lines
}

var userPromptURLRE = regexp.MustCompile(`https?://[^\s"'<>]+`)

// annotateUserMessageURLs links URL fragments to their complete destination
// after wrapping. A forced split leaves the continuation at the start of the
// next wrapped row, so the remaining URL is carried across rows (Rust #47360).
func annotateUserMessageURLs(lines []string, source string) []string {
	if len(lines) == 0 || !strings.Contains(source, "http") {
		return lines
	}
	urls := []string{}
	for _, raw := range userPromptURLRE.FindAllString(source, -1) {
		url, _ := tui.TrimWebURLToken(raw)
		if _, ok := tui.WebDestination(url); ok {
			urls = append(urls, url)
		}
	}
	if len(urls) == 0 {
		return lines
	}
	nextURLIndex := 0
	activeURLIndex := -1
	activeDestination := ""
	activeRemaining := ""
	out := make([]string, len(lines))
	for lineIndex, line := range lines {
		var builder strings.Builder
		cursor := 0
		if activeRemaining != "" {
			leading := 0
			for leading < len(line) && line[leading] == ' ' {
				leading++
			}
			matched := commonPrefixLen(line[leading:], activeRemaining)
			if matched > 0 {
				builder.WriteString(line[:leading])
				builder.WriteString(tui.OSC8Hyperlink(activeDestination, line[leading:leading+matched]))
				cursor = leading + matched
				activeRemaining = activeRemaining[matched:]
				if activeRemaining == "" {
					nextURLIndex = activeURLIndex + 1
					activeURLIndex = -1
					activeDestination = ""
				}
			} else {
				activeDestination = ""
				activeRemaining = ""
				activeURLIndex = -1
			}
		}
		rest := line[cursor:]
		for _, location := range userPromptURLRE.FindAllStringIndex(rest, -1) {
			raw := rest[location[0]:location[1]]
			token, trimStart := tui.TrimWebURLToken(raw)
			if token == "" {
				continue
			}
			start := cursor + location[0] + trimStart
			destination := ""
			destinationIndex := -1
			for index := nextURLIndex; index < len(urls); index++ {
				candidate := urls[index]
				if token == candidate || strings.HasPrefix(candidate, token) {
					destination = candidate
					destinationIndex = index
					break
				}
			}
			if destination == "" {
				for index, candidate := range urls {
					if token == candidate || strings.HasPrefix(candidate, token) {
						destination = candidate
						destinationIndex = index
						break
					}
				}
			}
			builder.WriteString(line[cursor:start])
			if destination == "" {
				builder.WriteString(token)
				cursor = start + len(token)
				continue
			}
			builder.WriteString(tui.OSC8Hyperlink(destination, token))
			cursor = start + len(token)
			if token != destination {
				activeDestination = destination
				activeRemaining = strings.TrimPrefix(destination, token)
				activeURLIndex = destinationIndex
			} else {
				nextURLIndex = destinationIndex + 1
			}
		}
		builder.WriteString(line[cursor:])
		out[lineIndex] = builder.String()
	}
	return out
}

func commonPrefixLen(left string, right string) int {
	limit := min(len(left), len(right))
	index := 0
	for index < limit && left[index] == right[index] {
		index++
	}
	return index
}

// styleUserMessageMentions wraps mention-like tokens ($name / @name) with cyan
// so tool/plugin mentions inside a user message are highlighted, mirroring the
// Rust TUI's cyan text-element rendering. Common environment variables (for
// example $HOME/$PATH) are left uncolored.
func styleUserMessageMentions(text string) string {
	if !strings.ContainsAny(text, "$@") {
		return text
	}
	if !tui.HistoryPromptAccentAllowed(tui.DetectStdoutColorLevel()) {
		// Rust #57554: without a resolved surface fill the terminal's default
		// foreground is kept instead of painting an element accent.
		return text
	}
	var sb strings.Builder
	cursor := 0
	for _, loc := range userMessageMentionRE.FindAllStringIndex(text, -1) {
		token := text[loc[0]:loc[1]]
		name := token[1:]
		if isCommonEnvVarName(name) || !isStandaloneMentionToken(text, loc[0], loc[1], token[0]) {
			continue
		}
		sb.WriteString(text[cursor:loc[0]])
		sb.WriteString(ansiUserMessageMention)
		sb.WriteString(token)
		sb.WriteString(ansiUserMessageReset)
		cursor = loc[1]
	}
	if cursor == 0 {
		return text
	}
	sb.WriteString(text[cursor:])
	return sb.String()
}

// isStandaloneMentionToken reports whether the token at [start,end) is a
// standalone mention rather than part of an identifier, email address, or path.
func isStandaloneMentionToken(text string, start int, end int, sigil byte) bool {
	if sigil == '$' {
		if start > 0 && (isMentionNameByte(text[start-1]) || text[start-1] == '.') {
			return false
		}
		if end < len(text) && (text[end] == '.' || text[end] == '/' || text[end] == '\\') {
			return false
		}
	}
	if sigil == '@' {
		if start > 0 && isMentionNameByte(text[start-1]) {
			return false
		}
	}
	if start > 0 {
		prev := text[start-1]
		if isMentionNameByte(prev) || prev == '.' || prev == '/' || prev == '\\' {
			return false
		}
	}
	if end < len(text) {
		next := text[end]
		if isMentionNameByte(next) {
			return false
		}
	}
	return true
}

func isMentionNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-'
}

func isCommonEnvVarName(name string) bool {
	switch strings.ToUpper(name) {
	case "PATH", "HOME", "USER", "SHELL", "PWD", "TMPDIR", "TEMP", "TMP", "LANG", "TERM", "XDG_CONFIG_HOME":
		return true
	default:
		return false
	}
}

func styleUserMessageLine(content string, width int) string {
	width = max(width, 1)
	padding := width - tui.DisplayWidth(utils.StripANSI(content))
	if padding < 0 {
		padding = 0
	}
	return tui.HistoryPromptSGR(tui.DetectStdoutColorLevel()) + content + strings.Repeat(" ", padding) + ansiUserMessageReset
}

// UserMessageSurfaceBlankLine is one full-width row painted with the user
// prompt surface. Consecutive prompts share a single such row so their
// background block stays contiguous (Rust #56280).
func UserMessageSurfaceBlankLine(width int) string {
	return styleUserMessageLine("", width)
}

func (c UserHistoryCell) RawLines() []string {
	lines := rawLinesFromSource(c.Message)
	if len(c.RemoteImageURLs) > 0 {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		for index := range c.RemoteImageURLs {
			lines = append(lines, localImageLabelText(index+1))
		}
	}
	return lines
}

type AgentMessageCell struct {
	Lines       []string
	IsFirstLine bool
	// Prewrapped marks lines that were already rendered to the target width
	// (for example streaming markdown output that carries ANSI styling). Such
	// lines are only prefixed rather than re-wrapped, which would otherwise
	// split an ANSI-colored token across display lines.
	Prewrapped bool
}

func NewAgentMessageCell(lines []string, isFirstLine bool) AgentMessageCell {
	return newAgentMessageCell(lines, isFirstLine, false)
}

func NewPrewrappedAgentMessageCell(lines []string, isFirstLine bool) AgentMessageCell {
	return newAgentMessageCell(lines, isFirstLine, true)
}

func newAgentMessageCell(lines []string, isFirstLine bool, prewrapped bool) AgentMessageCell {
	normalized := append([]string(nil), lines...)
	for i := range normalized {
		if strings.TrimSpace(normalized[i]) == "" {
			normalized[i] = ""
		}
	}
	return AgentMessageCell{Lines: normalized, IsFirstLine: isFirstLine, Prewrapped: prewrapped}
}

func (c AgentMessageCell) DisplayLines(width int) []string {
	out := []string{}
	for index, line := range c.Lines {
		initial := "  "
		if index == 0 && c.IsFirstLine {
			initial = "\u2022 "
		}
		if c.Prewrapped {
			out = append(out, initial+line)
			continue
		}
		out = append(out, tui.AdaptiveWrapLine(line, tui.WrapOptions{
			Width:            width,
			InitialIndent:    initial,
			SubsequentIndent: "  ",
			BreakWords:       true,
		})...)
	}
	return out
}

func (c AgentMessageCell) RawLines() []string {
	return plainLines(c.Lines)
}

type ReasoningSummaryCell struct {
	Content        string
	TranscriptOnly bool
}

func NewReasoningSummaryCell(content string, transcriptOnly bool) ReasoningSummaryCell {
	return ReasoningSummaryCell{Content: strings.TrimSpace(content), TranscriptOnly: transcriptOnly}
}

// SplitReasoningSummaryParts mirrors Rust split_reasoning_summary_parts: it
// separates a reasoning item's structured summary parts into the status header
// and the renderable content. A part whose body is only an empty HTML comment
// placeholder is dropped, keeping a leading placeholder's header as the
// fallback header; the joined content then drops its own leading
// `**header**` line when the remainder starts on a new line.
func SplitReasoningSummaryParts(reasoningParts []string) (header string, content string) {
	leadingEmptyPartHeader := ""
	hasLeadingEmptyPartHeader := false
	contentParts := make([]string, 0, len(reasoningParts))

	for _, part := range reasoningParts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		headerEnd, hasHeaderEnd := reasoningHeaderEnd(part)
		body := part
		if hasHeaderEnd {
			body = part[headerEnd:]
		}
		if strings.TrimSpace(body) == "<!-- -->" {
			if len(contentParts) == 0 && !hasLeadingEmptyPartHeader && hasHeaderEnd {
				leadingEmptyPartHeader = part[:headerEnd]
				hasLeadingEmptyPartHeader = true
			}
			continue
		}

		contentParts = append(contentParts, part)
	}

	content = strings.Join(contentParts, "\n\n")
	if content == "" {
		return leadingEmptyPartHeader, content
	}

	if afterOpen, ok := strings.CutPrefix(content, "**"); ok {
		if close := strings.Index(afterOpen, "**"); close >= 0 {
			afterCloseIdx := 2 + close + 2
			afterClose := content[afterCloseIdx:]
			if strings.HasPrefix(afterClose, "\n") || strings.HasPrefix(afterClose, "\r") {
				return content[:afterCloseIdx], afterClose
			}
		}
	}

	return leadingEmptyPartHeader, content
}

// reasoningHeaderEnd reports the byte offset just past a part's leading
// `**header**` markup, mirroring Rust's strip_prefix("**") + find("**") pair
// (the closing marker must not be the opening one).
func reasoningHeaderEnd(part string) (int, bool) {
	afterOpen, ok := strings.CutPrefix(part, "**")
	if !ok {
		return 0, false
	}
	close := strings.Index(afterOpen, "**")
	if close <= 0 {
		return 0, false
	}
	return close + 4, true
}

// NewReasoningSummaryBlock mirrors Rust new_reasoning_summary_block: a
// completed reasoning block is retained in the expanded transcript only.
func NewReasoningSummaryBlock(reasoningParts []string) ReasoningSummaryCell {
	_, content := SplitReasoningSummaryParts(reasoningParts)
	return NewReasoningSummaryCell(content, true)
}

func (c ReasoningSummaryCell) DisplayLines(width int) []string {
	if c.TranscriptOnly || c.Content == "" {
		return nil
	}
	return tui.AdaptiveWrapLine(c.Content, tui.WrapOptions{
		Width:            width,
		InitialIndent:    "\u2022 ",
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
}

func (c ReasoningSummaryCell) RawLines() []string {
	if c.TranscriptOnly {
		return nil
	}
	return rawLinesFromSource(c.Content)
}

func localImageLabelText(index int) string {
	if index <= 1 {
		return "[image]"
	}
	return "[image " + tui.FormatInt(int64(index)) + "]"
}

func trimTrailingBlankLines(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
