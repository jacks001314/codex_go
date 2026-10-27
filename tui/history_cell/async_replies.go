package historycell

import (
	"strings"

	"codex_go/tui"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/history_cell/async_replies.rs (#57292). A
// desktop async-question reply renders on the user-message surface as a
// shortened `↪ Re:` question caption followed by the complete Markdown answer.

const (
	asyncReplyCaptionPrefix = "\u21aa Re: "
	asyncReplyAccent        = "\x1b[38;5;12m"
	asyncReplyAccentEnd     = "\x1b[39m"
)

// AsyncQuestionReplyCell is the history cell for one or more replies.
type AsyncQuestionReplyCell struct {
	Replies []tui.AsyncQuestionReply
	ThemeID string
	CWD     string
}

// NewAsyncQuestionReplyCell builds the reply cell.
func NewAsyncQuestionReplyCell(replies []tui.AsyncQuestionReply, themeID string, cwd string) AsyncQuestionReplyCell {
	return AsyncQuestionReplyCell{
		Replies: append([]tui.AsyncQuestionReply(nil), replies...),
		ThemeID: themeID,
		CWD:     cwd,
	}
}

// DisplayLines renders each reply's caption and Markdown answer. The caption is
// limited to two rows with an ellipsis; the answer always keeps its full text.
func (c AsyncQuestionReplyCell) DisplayLines(width int) []string {
	width = max(width, 1)
	lines := make([]string, 0, len(c.Replies)*4)
	for index, reply := range c.Replies {
		if index > 0 {
			lines = append(lines, styleUserMessageLine("", width))
		}
		question := SanitizeUserText(reply.Question)
		captionWidth := max(width-tui.DisplayWidth(asyncReplyCaptionPrefix), 1)
		caption := markdownRows(question, captionWidth, c.ThemeID, c.CWD)
		if len(caption) == 0 {
			caption = []string{""}
		}
		overflow := len(caption) > 2
		if overflow {
			caption = caption[:2]
		}
		for rowIndex, row := range caption {
			prefix := strings.Repeat(" ", tui.DisplayWidth(asyncReplyCaptionPrefix))
			if rowIndex == 0 {
				prefix = asyncReplyAccent + "\u21aa " + asyncReplyAccentEnd + "Re: "
			}
			text := prefix + row
			if overflow && rowIndex == 1 {
				text = tui.TruncateWithEllipsis(text, width)
				if !strings.HasSuffix(text, "\u2026") {
					text = tui.TruncateWithEllipsis(text, max(width-1, 1)) + "\u2026"
				}
			}
			lines = append(lines, styleUserMessageLine(text, width))
		}

		answer := SanitizeUserText(reply.Answer)
		if strings.TrimSpace(answer) == "" {
			continue
		}
		lines = append(lines, styleUserMessageLine("", width))
		body := markdownRows(answer, max(width-2, 1), c.ThemeID, c.CWD)
		if len(body) == 0 {
			body = strings.Split(answer, "\n")
		}
		for _, row := range body {
			lines = append(lines, styleUserMessageLine("  "+row, width))
		}
	}
	return lines
}

// RawLines copies the readable question-and-answer text (Rust display_replies).
func (c AsyncQuestionReplyCell) RawLines() []string {
	raw := make([]string, 0, len(c.Replies))
	for _, reply := range c.Replies {
		raw = append(raw, "> "+reply.Question+"\n\n"+reply.Answer)
	}
	return strings.Split(strings.Join(raw, "\n\n"), "\n")
}

var _ HistoryCell = AsyncQuestionReplyCell{}

// markdownRows renders Markdown to rows, trimming blank display edges so the
// caption and answer start where the surface wants them.
func markdownRows(text string, width int, themeID string, cwd string) []string {
	rendered, err := markdown.RenderWithThemeCwd(text, max(width, 1), themeID, cwd)
	if err != nil || strings.TrimSpace(rendered) == "" {
		return nil
	}
	rows := strings.Split(strings.TrimRight(rendered, "\r\n"), "\n")
	start := 0
	for start < len(rows) && strings.TrimSpace(rows[start]) == "" {
		start++
	}
	end := len(rows)
	for end > start && strings.TrimSpace(rows[end-1]) == "" {
		end--
	}
	return rows[start:end]
}
