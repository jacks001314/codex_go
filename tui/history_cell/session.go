package historycell

import (
	"path/filepath"
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/session.rs.

type SessionHeaderHistoryCell struct {
	Version         string
	Model           string
	ReasoningEffort string
	Directory       string
	YoloMode        bool
}

func NewSessionHeader(model string, reasoningEffort string, directory string, version string) SessionHeaderHistoryCell {
	return SessionHeaderHistoryCell{
		Version:         strings.TrimSpace(version),
		Model:           strings.TrimSpace(model),
		ReasoningEffort: strings.TrimSpace(reasoningEffort),
		Directory:       strings.TrimSpace(directory),
	}
}

func (c SessionHeaderHistoryCell) WithYoloMode(yoloMode bool) SessionHeaderHistoryCell {
	c.YoloMode = yoloMode
	return c
}

func (c SessionHeaderHistoryCell) DisplayLines(width int) []string {
	if width <= 0 {
		return nil
	}
	// Rust #48562: every session header (new, resume, fork, clear-screen) uses
	// the compact borderless title/directory layout; the boxed model row is gone.
	lines := []string{
		"",
		"  >_ OpenAI Codex " + c.versionLabel(),
		"     " + c.formatDirectoryLimit(width-5),
	}
	if c.YoloMode {
		lines = append(lines, "  permissions: YOLO mode")
	}
	for i := range lines {
		lines[i] = tui.TruncateWithEllipsis(lines[i], width)
	}
	return lines
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

// formatDirectoryLimit mirrors Rust's `format_directory(Some(max_width))`: a zero
// or negative budget yields an empty path instead of Go's "no limit" sentinel.
func (c SessionHeaderHistoryCell) formatDirectoryLimit(maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	return c.formatDirectory(maxWidth)
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

// newSessionStartupHelpLines renders the first-event help block Rust shows
// beneath a new session header when the tooltip preference is enabled
// (#58127).
func newSessionStartupHelpLines() []string {
	return []string{
		"  To get started, describe a task or try one of these commands:",
		"",
		"  /init - create an AGENTS.md file with instructions for Codex",
		"  /status - show current session configuration",
		"  /permissions - choose what Codex is allowed to do",
		"  /model - choose what model and reasoning effort to use",
		"  /review - review any changes and find issues",
	}
}

// NewSessionInfo builds a session header cell. The startup help block is a
// tooltip too, so a first event renders it only when showTooltips is enabled;
// a later event renders the supplied tooltip instead (Rust #58127).
func NewSessionInfo(header SessionHeaderHistoryCell, isFirstEvent bool, showTooltips bool, tooltip string) SessionInfoCell {
	parts := []HistoryCell{header}
	if isFirstEvent && showTooltips {
		parts = append(parts, NewPlainHistoryCell(newSessionStartupHelpLines()))
	} else if !isFirstEvent && strings.TrimSpace(tooltip) != "" {
		parts = append(parts, NewPrefixedWrappedHistoryCell("Tip: "+strings.TrimSpace(tooltip), "  ", "  "))
	}
	return SessionInfoCell{Parts: parts}
}

// NewSessionInfoWithTooltipLines inserts an already rendered, width-aware
// tooltip beneath the session header. The rendered lines may carry ANSI styling
// and OSC 8 hyperlinks, so the transcript must not re-wrap them. The startup
// help block is gated by showTooltips and only belongs to a first event
// (Rust #58127).
func NewSessionInfoWithTooltipLines(header SessionHeaderHistoryCell, isFirstEvent bool, showTooltips bool, tooltipLines []string) SessionInfoCell {
	parts := []HistoryCell{header}
	if isFirstEvent && showTooltips {
		parts = append(parts, NewPlainHistoryCell(newSessionStartupHelpLines()))
	} else if !isFirstEvent && len(tooltipLines) > 0 {
		parts = append(parts, NewPlainHistoryCell(tooltipLines))
	}
	return SessionInfoCell{Parts: parts}
}

func (c SessionInfoCell) DisplayLines(width int) []string {
	return joinCellLines(c.Parts, width, false)
}

func (c SessionInfoCell) RawLines() []string {
	return joinCellLines(c.Parts, 0, true)
}
