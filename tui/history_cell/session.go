package historycell

import (
	"path/filepath"
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/session.rs.

const SessionHeaderMaxInnerWidth = 56

type SessionHeaderHistoryCell struct {
	Version         string
	Model           string
	ReasoningEffort string
	ShowFastStatus  bool
	Directory       string
	YoloMode        bool
}

func NewSessionHeader(model string, reasoningEffort string, showFastStatus bool, directory string, version string) SessionHeaderHistoryCell {
	return SessionHeaderHistoryCell{
		Version:         strings.TrimSpace(version),
		Model:           strings.TrimSpace(model),
		ReasoningEffort: strings.TrimSpace(reasoningEffort),
		ShowFastStatus:  showFastStatus,
		Directory:       strings.TrimSpace(directory),
	}
}

func (c SessionHeaderHistoryCell) WithYoloMode(yoloMode bool) SessionHeaderHistoryCell {
	c.YoloMode = yoloMode
	return c
}

func (c SessionHeaderHistoryCell) DisplayLines(width int) []string {
	if width < 4 {
		return nil
	}
	innerWidth := min(width-4, SessionHeaderMaxInnerWidth)
	if innerWidth < 1 {
		return nil
	}
	lines := []string{
		">_ OpenAI Codex " + c.versionLabel(),
		"",
		c.modelLine(),
		c.directoryLine(innerWidth),
	}
	if c.YoloMode {
		lines = append(lines, "permissions: YOLO mode")
	}
	for i := range lines {
		lines[i] = tui.TruncateWithEllipsis(lines[i], innerWidth)
	}
	return historyBorder(lines, innerWidth)
}

func (c SessionHeaderHistoryCell) RawLines() []string {
	lines := []string{
		"OpenAI Codex " + c.versionLabel(),
		"model: " + strings.TrimSpace(c.Model+reasoningSuffix(c.ReasoningEffort)),
		"directory: " + c.formatDirectory(0),
	}
	if c.YoloMode {
		lines = append(lines, "permissions: YOLO mode")
	}
	return lines
}

func (c SessionHeaderHistoryCell) versionLabel() string {
	version := strings.TrimSpace(c.Version)
	if version == "" {
		version = "dev"
	}
	return "(v" + version + ")"
}

func (c SessionHeaderHistoryCell) modelLine() string {
	line := "model: " + firstNonEmptyHistory(c.Model, "default")
	if c.ReasoningEffort != "" {
		line += " " + c.ReasoningEffort
	}
	if c.ShowFastStatus {
		line += "   fast"
	}
	line += "   /model to change"
	return line
}

func (c SessionHeaderHistoryCell) directoryLine(innerWidth int) string {
	prefix := "directory: "
	maxPathWidth := innerWidth - tui.DisplayWidth(prefix)
	if maxPathWidth < 1 {
		maxPathWidth = 1
	}
	return prefix + c.formatDirectory(maxPathWidth)
}

func (c SessionHeaderHistoryCell) formatDirectory(maxWidth int) string {
	dir := strings.TrimSpace(c.Directory)
	if dir == "" {
		dir = "."
	}
	if clean, err := filepath.Abs(dir); err == nil {
		dir = filepath.Clean(clean)
	}
	if maxWidth > 0 && tui.DisplayWidth(dir) > maxWidth {
		return tui.CenterTruncatePath(dir, maxWidth)
	}
	return dir
}

func reasoningSuffix(reasoning string) string {
	reasoning = strings.TrimSpace(reasoning)
	if reasoning == "" {
		return ""
	}
	return " " + reasoning
}

type SessionInfoCell struct {
	Parts []HistoryCell
}

func NewSessionInfo(header SessionHeaderHistoryCell, isFirstEvent bool, tooltip string) SessionInfoCell {
	parts := []HistoryCell{header}
	if isFirstEvent {
		parts = append(parts, NewPlainHistoryCell([]string{
			"  To get started, describe a task or try one of these commands:",
			"",
			"  /init - create an AGENTS.md file with instructions for Codex",
			"  /status - show current session configuration",
			"  /permissions - choose what Codex is allowed to do",
			"  /model - choose what model and reasoning effort to use",
			"  /review - review any changes and find issues",
		}))
	} else if strings.TrimSpace(tooltip) != "" {
		parts = append(parts, NewPrefixedWrappedHistoryCell("Tip: "+strings.TrimSpace(tooltip), "  ", "  "))
	}
	return SessionInfoCell{Parts: parts}
}

func (c SessionInfoCell) DisplayLines(width int) []string {
	return joinCellLines(c.Parts, width, false)
}

func (c SessionInfoCell) RawLines() []string {
	return joinCellLines(c.Parts, 0, true)
}

func historyBorder(lines []string, innerWidth int) []string {
	out := []string{"\u256d" + strings.Repeat("\u2500", innerWidth+2) + "\u256e"}
	for _, line := range lines {
		out = append(out, "\u2502 "+padRightRunes(line, innerWidth)+" \u2502")
	}
	out = append(out, "\u2570"+strings.Repeat("\u2500", innerWidth+2)+"\u256f")
	return out
}
