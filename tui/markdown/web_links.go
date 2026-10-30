package markdown

import (
	"os"
	"strings"

	"codex_go/shell"
	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/markdown_render/web_links.rs (#40471).

var webLinkTerminalSupported = func() bool {
	if !stdoutIsTerminal() || strings.TrimSpace(os.Getenv("STY")) != "" {
		return false
	}
	return webLinkLabelsHiddenForTerminal(shell.CurrentTerminalInfo(), os.Getenv("TERM"))
}

var stdoutIsTerminal = func() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// hideWebLinkDestination reports whether a non-empty web-link label can replace
// the repeated destination. Unknown terminals and multiplexers keep the URL.
func hideWebLinkDestination(destination string) bool {
	if _, ok := codextui.WebDestination(destination); !ok {
		return false
	}
	return webLinkTerminalSupported()
}

func webLinkLabelsHiddenForTerminal(info *shell.TerminalInfo, term string) bool {
	if info == nil || info.Multiplexer != nil {
		return false
	}
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "dumb" || strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux") {
		return false
	}
	switch info.Name {
	case shell.TerminalGhostty,
		shell.TerminalIterm2,
		shell.TerminalWezTerm,
		shell.TerminalRex,
		shell.TerminalKitty,
		shell.TerminalVSCode,
		shell.TerminalAlacritty,
		shell.TerminalWindowsTerminal,
		shell.TerminalKonsole,
		shell.TerminalGnomeTerminal,
		shell.TerminalVTE:
		return true
	default:
		return false
	}
}
