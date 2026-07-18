package tui

// Rust parity: codex-rs/tui/src/resize_reflow_cap.rs.

const (
	DefaultTerminalResizeReflowFallbackMaxRows = 5_000
	VSCodeResizeReflowMaxRows                  = 1_000
	WindowsTerminalResizeReflowMaxRows         = 9_001
	WezTermResizeReflowMaxRows                 = 3_500
	AlacrittyResizeReflowMaxRows               = 10_000
)

type TerminalName string

const (
	TerminalVSCode          TerminalName = "vscode"
	TerminalWindowsTerminal TerminalName = "windows-terminal"
	TerminalWezTerm         TerminalName = "wezterm"
	TerminalAlacritty       TerminalName = "alacritty"
	TerminalAppleTerminal   TerminalName = "apple-terminal"
	TerminalGhostty         TerminalName = "ghostty"
	TerminalITerm2          TerminalName = "iterm2"
	TerminalWarp            TerminalName = "warp"
	TerminalKitty           TerminalName = "kitty"
	TerminalKonsole         TerminalName = "konsole"
	TerminalGnome           TerminalName = "gnome-terminal"
	TerminalVTE             TerminalName = "vte"
	TerminalDumb            TerminalName = "dumb"
	TerminalUnknown         TerminalName = "unknown"
)

type ResizeReflowMaxRowsMode int

const (
	ResizeReflowAuto ResizeReflowMaxRowsMode = iota
	ResizeReflowDisabled
	ResizeReflowLimit
)

type ResizeReflowConfig struct {
	Mode  ResizeReflowMaxRowsMode
	Limit int
}

func DefaultResizeReflowConfig() ResizeReflowConfig {
	return ResizeReflowConfig{Mode: ResizeReflowAuto}
}

func ResizeReflowMaxRowsFor(config ResizeReflowConfig, terminalName TerminalName, runningInVSCodeTerminal bool) (int, bool) {
	switch config.Mode {
	case ResizeReflowDisabled:
		return 0, false
	case ResizeReflowLimit:
		if config.Limit < 0 {
			return 0, true
		}
		return config.Limit, true
	default:
		return AutoResizeReflowMaxRows(terminalName, runningInVSCodeTerminal), true
	}
}

func AutoResizeReflowMaxRows(terminalName TerminalName, runningInVSCodeTerminal bool) int {
	if runningInVSCodeTerminal {
		return VSCodeResizeReflowMaxRows
	}
	switch terminalName {
	case TerminalVSCode:
		return VSCodeResizeReflowMaxRows
	case TerminalWindowsTerminal:
		return WindowsTerminalResizeReflowMaxRows
	case TerminalWezTerm:
		return WezTermResizeReflowMaxRows
	case TerminalAlacritty:
		return AlacrittyResizeReflowMaxRows
	default:
		return DefaultTerminalResizeReflowFallbackMaxRows
	}
}
