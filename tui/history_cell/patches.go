package historycell

import (
	"path/filepath"
	"strconv"
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/patches.rs.

type PatchHistoryCell struct {
	Changes map[string]tui.FileChange
	CWD     string
	Theme   string
}

func NewPatchEvent(changes map[string]tui.FileChange, cwd string) PatchHistoryCell {
	return newPatchEventTheme(changes, cwd, "dark")
}

func NewPatchEventWithTheme(changes map[string]tui.FileChange, cwd string, theme string) PatchHistoryCell {
	return newPatchEventTheme(changes, cwd, theme)
}

func newPatchEventTheme(changes map[string]tui.FileChange, cwd string, theme string) PatchHistoryCell {
	cloned := make(map[string]tui.FileChange, len(changes))
	for path, change := range changes {
		cloned[path] = change
	}
	return PatchHistoryCell{Changes: cloned, CWD: strings.TrimSpace(cwd), Theme: theme}
}

func (c PatchHistoryCell) DisplayLines(width int) []string {
	return tui.CreateDiffSummary(c.Changes, c.CWD, width, c.Theme)
}

func (c PatchHistoryCell) RawLines() []string {
	return tui.CreateDiffSummary(c.Changes, c.CWD, 120, c.Theme)
}

func NewPatchApplyFailure(stderr string) PlainHistoryCell {
	return NewPlainHistoryCell(patchApplyFailureLines(stderr, 5))
}

func patchApplyFailureLines(stderr string, lineLimit int) []string {
	lines := strings.Split(strings.ReplaceAll(stderr, "\r\n", "\n"), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if lineLimit < 1 {
		lineLimit = 1
	}
	selected := lines
	if len(lines) > lineLimit*2 {
		omitted := len(lines) - lineLimit*2
		selected = append([]string(nil), lines[:lineLimit]...)
		selected = append(selected, "… +"+strconv.Itoa(omitted)+" lines (ctrl + t to view transcript)")
		selected = append(selected, lines[len(lines)-lineLimit:]...)
	}
	out := []string{"\u2718 Failed to apply patch"}
	for _, line := range selected {
		prefix := "  "
		if len(out) == 1 {
			prefix = "| "
		}
		out = append(out, prefix+line)
	}
	return out
}

func NewViewImageToolCall(path string, cwd string) PlainHistoryCell {
	displayPath := strings.TrimSpace(path)
	if displayPath != "" && cwd != "" {
		if abs, err := filepath.Abs(displayPath); err == nil {
			if rel, err := filepath.Rel(cwd, abs); err == nil && !strings.HasPrefix(rel, "..") {
				displayPath = filepath.ToSlash(rel)
			}
		}
	}
	return NewPlainHistoryCell([]string{
		"\u2022 Viewed Image",
		"  \u2514 " + displayPath,
	})
}

func NewImageGenerationCall(callID string, status string, revisedPrompt string, savedPath string) PlainHistoryCell {
	detail := firstNonEmptyHistory(revisedPrompt, callID)
	heading := "\u2022 Generated Image:"
	if strings.EqualFold(strings.TrimSpace(status), "failed") {
		heading = "\u2717 Image generation failed"
	}
	lines := []string{heading, "  \u2514 " + detail}
	if strings.TrimSpace(savedPath) != "" {
		lines = append(lines, "  \u2514 Saved to: "+strings.TrimSpace(savedPath))
	}
	return NewPlainHistoryCell(lines)
}
