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
	// Greeting is the session's selected startup greeting (Rust's shared
	// `OnceLock<Greeting>`). When set, the header appends the greeting beneath
	// the directory line.
	Greeting string
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

// WithGreeting binds the session's chosen greeting to the header
// (Rust `history_cell::set_session_greeting`).
func (c SessionHeaderHistoryCell) WithGreeting(greeting string) SessionHeaderHistoryCell {
	c.Greeting = strings.TrimSpace(greeting)
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
		"  >_ gcode " + c.versionLabel(),
		"     " + c.formatDirectoryLimit(width-5),
	}
	if c.YoloMode {
		lines = append(lines, "  permissions: YOLO mode")
	}
	if c.Greeting != "" {
		// The tip/help that follows has its own normal composite separator.
		lines = append(lines, "", "  "+c.Greeting)
	}
	for i := range lines {
		lines[i] = tui.TruncateWithEllipsis(lines[i], width)
	}
	return lines
}

func (c SessionHeaderHistoryCell) RawLines() []string {
	if c.Greeting != "" {
		// Rust's greeting raw output is the display lines at maximum width.
		return c.DisplayLines(1 << 30)
	}
	lines := []string{
		"gcode " + c.versionLabel(),
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

