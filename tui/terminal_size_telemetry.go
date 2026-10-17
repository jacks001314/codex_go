package tui

import (
	"os"

	"github.com/charmbracelet/x/term"

	"codex_go/config"
	"codex_go/metrics"
	"codex_go/shell"
)

// Rust parity: codex-rs/tui/src/terminal_size_telemetry.rs (#53859). Terminal
// cell sizes travel as coarse buckets, never as exact geometry, and the
// observation reuses the launch context captured for codex.tui.start.

// TUIStartMetric mirrors Rust's `codex.tui.start` counter.
const TUIStartMetric = "codex.tui.start"

// TerminalSizeMetric mirrors Rust's `codex.tui.terminal_size` counter.
const TerminalSizeMetric = "codex.tui.terminal_size"

// Terminal column/row bucket values mirror Rust's `tags`. `<` and `+` are
// rejected by metrics tag validation, so the ranges stay words and digits.
const (
	TerminalColumnsTag = "terminal_columns"
	TerminalRowsTag    = "terminal_rows"
)

// TerminalSizeTags returns the two bucketed size tags for a terminal geometry.
func TerminalSizeTags(width, height int) map[string]string {
	return map[string]string{
		TerminalColumnsTag: terminalColumnBucket(width),
		TerminalRowsTag:    terminalRowBucket(height),
	}
}

func terminalColumnBucket(width int) string {
	switch {
	case width <= 0:
		return "unknown"
	case width < 40:
		return "under-40"
	case width < 80:
		return "40-79"
	case width < 120:
		return "80-119"
	case width < 240:
		return "120-239"
	case width < 480:
		return "240-479"
	default:
		return "480-and-up"
	}
}

func terminalRowBucket(height int) string {
	switch {
	case height <= 0:
		return "unknown"
	case height < 15:
		return "under-15"
	case height < 24:
		return "15-23"
	case height < 40:
		return "24-39"
	case height < 80:
		return "40-79"
	case height < 120:
		return "80-119"
	default:
		return "120-and-up"
	}
}

// TerminalSizeTelemetryEnabled mirrors Rust's `enabled`: analytics consent
// gates the built-in Statsig exporter only. An explicitly configured OTLP
// collector keeps observing even when analytics is disabled.
func TerminalSizeTelemetryEnabled(exporter config.OtelExporterKind, analyticsEnabled *bool) bool {
	switch exporter.Kind {
	case config.OtelExporterKindNone, "":
		return false
	case config.OtelExporterKindStatsig:
		return analyticsEnabled == nil || *analyticsEnabled
	default:
		return true
	}
}

// TerminalSizeRead reports the process terminal's current geometry, or zeros
// when it cannot be queried.
func TerminalSizeRead() (int, int) {
	width, height, err := term.GetSize(os.Stdout.Fd())
	if err != nil || width <= 0 || height <= 0 {
		return 0, 0
	}
	return width, height
}

// TerminalTelemetryName maps a detected terminal to the fixed category Rust
// records (never the versioned or user-provided identifier).
func TerminalTelemetryName(info *shell.TerminalInfo) string {
	if info == nil {
		return "unknown"
	}
	switch info.Name {
	case shell.TerminalAppleTerminal:
		return "apple_terminal"
	case shell.TerminalGhostty:
		return "ghostty"
	case shell.TerminalIterm2:
		return "iterm2"
	case shell.TerminalWarp:
		return "warp"
	case shell.TerminalVSCode:
		return "vscode"
	case shell.TerminalWezTerm:
		return "wezterm"
	case shell.TerminalKitty:
		return "kitty"
	case shell.TerminalAlacritty:
		return "alacritty"
	case shell.TerminalKonsole:
		return "konsole"
	case shell.TerminalGnomeTerminal:
		return "gnome_terminal"
	case shell.TerminalVTE:
		return "vte"
	case shell.TerminalWindowsTerminal:
		return "windows_terminal"
	case shell.TerminalDumb:
		return "dumb"
	default:
		return "unknown"
	}
}

// TerminalMultiplexerTag maps the detected multiplexer to Rust's bounded tag.
func TerminalMultiplexerTag(info *shell.TerminalInfo) string {
	if info == nil || info.Multiplexer == nil {
		return "none"
	}
	switch info.Multiplexer.Name {
	case shell.MultiplexerTmux:
		return "tmux"
	case shell.MultiplexerZellij:
		return "zellij"
	default:
		return "none"
	}
}

// RecordTerminalSize mirrors Rust's `record`: append the size buckets and
// observation reason to the launch context and record one counter. Analytics
// consent is applied to the active exporter, so a provider replacement cannot
// bypass it.
func RecordTerminalSize(context map[string]string, analyticsEnabled bool, reason string, width, height int) {
	recorder := metrics.Global()
	if recorder == nil || len(context) == 0 {
		return
	}
	tags := make(map[string]string, len(context)+3)
	for key, value := range context {
		tags[key] = value
	}
	for key, value := range TerminalSizeTags(width, height) {
		tags[key] = value
	}
	tags["observation_reason"] = reason
	if counter, ok := recorder.(interface {
		CounterWithAnalyticsConsent(name string, inc int, tags map[string]string, analyticsEnabled bool)
	}); ok {
		counter.CounterWithAnalyticsConsent(TerminalSizeMetric, 1, tags, analyticsEnabled)
		return
	}
	// An unknown recorder cannot enforce consent, so a disabled consent state
	// suppresses the observation instead of leaking it.
	if !analyticsEnabled {
		return
	}
	recorder.Counter(TerminalSizeMetric, 1, tags)
}
