package historycell

import (
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/async_questions.rs (#57292).
// Agent messages that carry structured async questions render each question
// title with an accent `?` prefix and keep the following suggestions as the
// body of that question. If the metadata and visible Markdown diverge, the
// usual agent bullet rendering wins.

type asyncQuestionTitleRange struct {
	start int
	end   int
}

// RenderAsyncQuestionMarkdown renders an async agent message from its Markdown
// source. The returned rows already include the `? ` and body gutters, matching
// Rust's async_questions::render.
func RenderAsyncQuestionMarkdown(source string, questions []tui.MessageAsyncQuestion, width int, themeID string, cwd string) []string {
	titles := asyncQuestionTitleRanges(source, questions)
	if len(titles) == 0 {
		return nil
	}
	width = max(width, 3)
	contentWidth := width - 2
	accent := tui.ThemeAccentSGR(themeID, tui.DetectStdoutColorLevel())
	if accent == "" {
		accent = "\x1b[38;5;12m"
	}
	lines := make([]string, 0, len(titles)*4)
	for index, title := range titles {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		heading := markdownRows(source[title.start:title.end], contentWidth, themeID, cwd)
		if len(heading) == 0 {
			heading = []string{""}
		}
		for rowIndex, row := range heading {
			prefix := "  "
			if rowIndex == 0 {
				prefix = "? "
			}
			lines = append(lines, prefix+styleAsyncQuestionHeading(row, accent))
		}

		end := len(source)
		if index+1 < len(titles) {
			end = titles[index+1].start
		}
		body := strings.Trim(source[title.end:end], "\n")
		if body == "" {
			continue
		}
		lines = append(lines, "")
		for _, row := range markdownRows(body, contentWidth, themeID, cwd) {
			lines = append(lines, "  "+row)
		}
	}
	return lines
}

func styleAsyncQuestionHeading(row string, accent string) string {
	if strings.TrimSpace(row) == "" {
		return row
	}
	return accent + row + "\x1b[0m"
}

// asyncQuestionTitleRanges mirrors Rust's title_ranges. It only accepts the
// question metadata when the source begins with the same title and each
// suggestion follows as a `- ` bullet, stopping at the first mismatch.
func asyncQuestionTitleRanges(source string, questions []tui.MessageAsyncQuestion) []asyncQuestionTitleRange {
	titles := make([]asyncQuestionTitleRange, 0, len(questions))
	rest := source
	for _, question := range questions {
		rest = strings.TrimLeft(rest, "\n")
		title := strings.Trim(strings.ReplaceAll(question.Title, "\r\n", "\n"), "\n")
		if !strings.HasPrefix(rest, title) {
			break
		}
		start := len(source) - len(rest)
		titles = append(titles, asyncQuestionTitleRange{start: start, end: start + len(title)})
		rest = rest[len(title):]
		optionMismatch := false
		for _, option := range question.Options {
			option = strings.ReplaceAll(option, "\r\n", "\n")
			trimmed := strings.TrimLeft(rest, "\n")
			afterBullet, ok := strings.CutPrefix(trimmed, "- ")
			if !ok || !strings.HasPrefix(afterBullet, option) {
				optionMismatch = true
				break
			}
			rest = afterBullet[len(option):]
		}
		if optionMismatch {
			break
		}
	}
	return titles
}
