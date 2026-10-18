package markdown

import (
	"strings"
	"testing"

	"codex_go/shell"
	codextui "codex_go/tui"
	"codex_go/utils"
)

func withWebLinkTerminalSupport(t *testing.T, supported bool) {
	t.Helper()
	previousSupported := webLinkTerminalSupported
	previousStdout := stdoutIsTerminal
	webLinkTerminalSupported = func() bool { return supported }
	stdoutIsTerminal = func() bool { return true }
	t.Cleanup(func() {
		webLinkTerminalSupported = previousSupported
		stdoutIsTerminal = previousStdout
	})
}

// TestWebLinkLabelsHideDestinationInSupportedTerminalsLikeRust covers Rust
// #40471's label-only presentation with a preserved OSC-8 target.
func TestWebLinkLabelsHideDestinationInSupportedTerminalsLikeRust(t *testing.T) {
	withWebLinkTerminalSupport(t, true)
	rendered, err := RenderWithTheme("See [docs](https://example.com/docs) for details.", 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "\x1b]8;;https://example.com/docs\x07docs\x1b]8;;\x07") {
		t.Fatalf("label hyperlink missing:\n%q", rendered)
	}
	visible := strings.TrimSpace(codextui.StripOSC8(utils.StripANSI(rendered)))
	if visible != "See docs for details." {
		t.Fatalf("visible link text = %q", visible)
	}
	if strings.Contains(visible, "https://example.com/docs") {
		t.Fatalf("supported terminal repeated the destination: %q", visible)
	}
}

// TestWebLinkLabelsKeepDestinationInUnknownTerminalsLikeRust covers the
// conservative fallback.
func TestWebLinkLabelsKeepDestinationInUnknownTerminalsLikeRust(t *testing.T) {
	withWebLinkTerminalSupport(t, false)
	rendered, err := RenderWithTheme("See [docs](https://example.com/docs).", 80, "")
	if err != nil {
		t.Fatal(err)
	}
	visible := codextui.StripOSC8(utils.StripANSI(rendered))
	if !strings.Contains(visible, "docs") || !strings.Contains(visible, "https://example.com/docs") {
		t.Fatalf("fallback dropped label or destination: %q", visible)
	}
}

// TestWebLinkEmptyLabelKeepsDestinationLikeRust covers empty-label fallback.
func TestWebLinkEmptyLabelKeepsDestinationLikeRust(t *testing.T) {
	withWebLinkTerminalSupport(t, true)
	rendered, err := RenderWithTheme("[](https://example.com/docs)", 80, "")
	if err != nil {
		t.Fatal(err)
	}
	if visible := codextui.StripOSC8(utils.StripANSI(rendered)); !strings.Contains(visible, "https://example.com/docs") {
		t.Fatalf("empty label hid the only URL: %q", visible)
	}
}

// TestWebLinkTerminalDetectionMatchesRust covers the terminal allowlist and
// multiplexer exclusions.
func TestWebLinkTerminalDetectionMatchesRust(t *testing.T) {
	if !webLinkLabelsHiddenForTerminal(&shell.TerminalInfo{Name: shell.TerminalIterm2}, "xterm-256color") {
		t.Fatal("iTerm2 should hide destinations")
	}
	if !webLinkLabelsHiddenForTerminal(&shell.TerminalInfo{Name: shell.TerminalWindowsTerminal}, "") {
		t.Fatal("Windows Terminal should hide destinations")
	}
	if webLinkLabelsHiddenForTerminal(&shell.TerminalInfo{
		Name: shell.TerminalIterm2,
		Multiplexer: &shell.Multiplexer{
			Name: shell.MultiplexerTmux,
		},
	}, "") {
		t.Fatal("tmux should keep destinations")
	}
	if webLinkLabelsHiddenForTerminal(&shell.TerminalInfo{Name: shell.TerminalIterm2}, "screen-256color") {
		t.Fatal("screen should keep destinations")
	}
	if webLinkLabelsHiddenForTerminal(&shell.TerminalInfo{Name: shell.TerminalAppleTerminal}, "") {
		t.Fatal("Apple Terminal should keep destinations")
	}
}
