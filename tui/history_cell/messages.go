package historycell

import (
	"regexp"
	"strings"

	"codex_go/tui"
	"codex_go/utils"
)

// Rust parity: codex-rs/tui/src/history_cell/messages.rs.

const (
	ansiUserMessageBackground = "\x1b[48;5;235m"
	ansiUserMessagePrefix     = "\x1b[1;2m"
	ansiUserMessagePrefixEnd  = "\x1b[22m"
	ansiUserMessageReset      = "\x1b[0m"
	ansiUserMessageMention    = "\x1b[36m"
)

var userMessageMentionRE = regexp.MustCompile(`[$@][A-Za-z0-9][A-Za-z0-9_-]*`)

type TextElement struct {
	Start int
	End   int
}

type UserHistoryCell struct {
	Message         string
	TextElements    []TextElement
	LocalImagePaths []string
	RemoteImageURLs []string
}

func NewUserPrompt(message string, textElements []TextElement, localImagePaths []string, remoteImageURLs []string) UserHistoryCell {
	return UserHistoryCell{
		Message:         message,
		TextElements:    append([]TextElement(nil), textElements...),
		LocalImagePaths: append([]string(nil), localImagePaths...),
		RemoteImageURLs: append([]string(nil), remoteImageURLs...),
	}
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
	wrappedMessage := []string{}
	if message != "" {
		for _, line := range strings.Split(message, "\n") {
			wrappedMessage = append(wrappedMessage, tui.WrapLine(line, tui.WrapOptions{
				Width:      wrapWidth,
				BreakWords: true,
			})...)
		}
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
			prefix = ansiUserMessagePrefix + "\u203a " + ansiUserMessagePrefixEnd
		}
		lines = append(lines, styleUserMessageLine(prefix+styleUserMessageMentions(line), width))
	}
	lines = append(lines, styleUserMessageLine("", width))
	return lines
}

// styleUserMessageMentions wraps mention-like tokens ($name / @name) with cyan
// so tool/plugin mentions inside a user message are highlighted, mirroring the
// Rust TUI's cyan text-element rendering. Common environment variables (for
// example $HOME/$PATH) are left uncolored.
func styleUserMessageMentions(text string) string {
	if !strings.ContainsAny(text, "$@") {
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
	return ansiUserMessageBackground + content + strings.Repeat(" ", padding) + ansiUserMessageReset
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
