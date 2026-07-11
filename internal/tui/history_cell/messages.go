package historycell

import (
	"strings"

	"codex_go/internal/tui"
	"codex_go/internal/utils"
)

// Rust parity: codex-rs/tui/src/history_cell/messages.rs.

const (
	ansiUserMessageBackground = "\x1b[48;5;235m"
	ansiUserMessagePrefix     = "\x1b[1;2m"
	ansiUserMessagePrefixEnd  = "\x1b[22m"
	ansiUserMessageReset      = "\x1b[0m"
)

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
			wrappedMessage = append(wrappedMessage, tui.AdaptiveWrapLine(line, tui.WrapOptions{
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
		lines = append(lines, styleUserMessageLine(prefix+line, width))
	}
	lines = append(lines, styleUserMessageLine("", width))
	return lines
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
}

func NewAgentMessageCell(lines []string, isFirstLine bool) AgentMessageCell {
	return AgentMessageCell{Lines: append([]string(nil), lines...), IsFirstLine: isFirstLine}
}

func (c AgentMessageCell) DisplayLines(width int) []string {
	out := []string{}
	for index, line := range c.Lines {
		initial := "  "
		if index == 0 && c.IsFirstLine {
			initial = "\u2022 "
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
