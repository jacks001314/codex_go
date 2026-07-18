package historycell

import (
	"path/filepath"
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/patches.rs.

type PatchHistoryCell struct {
	Changes map[string]tui.FileChange
	CWD     string
}

func NewPatchEvent(changes map[string]tui.FileChange, cwd string) PatchHistoryCell {
	cloned := make(map[string]tui.FileChange, len(changes))
	for path, change := range changes {
		cloned[path] = change
	}
	return PatchHistoryCell{Changes: cloned, CWD: strings.TrimSpace(cwd)}
}

func (c PatchHistoryCell) DisplayLines(width int) []string {
	return tui.CreateDiffSummary(c.Changes, c.CWD, width)
}

func (c PatchHistoryCell) RawLines() []string {
	return tui.CreateDiffSummary(c.Changes, c.CWD, 120)
}

func NewPatchApplyFailure(stderr string) PlainHistoryCell {
	lines := []string{"\u2718 Failed to apply patch"}
	for _, line := range truncateToolResultLines(stderr, 120) {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, "| "+line)
		}
	}
	return NewPlainHistoryCell(lines)
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
