package tui

// Mirrors Rust #61919 terminal_probe startup_replay: a direct herdr session is
// asked whether it supports Kitty graphics, the reply alone decides the session
// protocol, and the query never reaches the composer.

import (
	"strings"
	"testing"

	"codex_go/shell"
)

func TestFindKittyGraphicsReportReadsOnlyTheProbeReplyLikeRust(t *testing.T) {
	cases := []struct {
		name      string
		buffer    string
		supported bool
		found     bool
	}{
		{name: "ok", buffer: "\x1b_Gi=31;OK\x1b\\", supported: true, found: true},
		{name: "rejected", buffer: "\x1b_Gi=31;ENOENT\x1b\\", supported: false, found: true},
		{name: "other-image-id", buffer: "\x1b_Gi=32;OK\x1b\\"},
		{name: "incomplete-payload", buffer: "\x1b_Gi=31;OK"},
		{name: "no-terminator", buffer: "\x1b_Gi=31;OKX"},
		{name: "after-da1", buffer: "\x1b[?62;c\x1b_Gi=31;OK\x1b\\\x1b]11;rgb:1/1/1\x1b\\", supported: true, found: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			supported, found := FindKittyGraphicsReport([]byte(tc.buffer))
			if found != tc.found || supported != tc.supported {
				t.Fatalf("report = (%v,%v), want (%v,%v)", supported, found, tc.supported, tc.found)
			}
		})
	}
}

func TestShouldProbeKittyGraphicsOnlyForDirectHerdrLikeRust(t *testing.T) {
	env := func(values map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			value, ok := values[key]
			return value, ok
		}
	}
	noMultiplexer := func() *shell.TerminalInfo { return &shell.TerminalInfo{} }
	tmuxInfo := func() *shell.TerminalInfo {
		mux := shell.Multiplexer{Name: shell.MultiplexerTmux}
		return &shell.TerminalInfo{Multiplexer: &mux}
	}
	cases := []struct {
		name   string
		info   func() *shell.TerminalInfo
		env    map[string]string
		expect bool
	}{
		{name: "herdr-direct", info: noMultiplexer, env: map[string]string{"TERM_PROGRAM": "herdr"}, expect: true},
		{name: "herdr-tmux-info", info: tmuxInfo, env: map[string]string{"TERM_PROGRAM": "herdr"}},
		{name: "herdr-tmux-env", info: noMultiplexer, env: map[string]string{"TERM_PROGRAM": "herdr", "TMUX": "/tmp/tmux-1/default,1,0"}},
		{name: "herdr-zellij-env", info: noMultiplexer, env: map[string]string{"TERM_PROGRAM": "herdr", "ZELLIJ": "0"}},
		{name: "kitty-program", info: noMultiplexer, env: map[string]string{"TERM_PROGRAM": "kitty"}},
		{name: "no-program", info: noMultiplexer, env: map[string]string{"TERM": "xterm-256color"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldProbeKittyGraphics(tc.info, env(tc.env)); got != tc.expect {
				t.Fatalf("shouldProbeKittyGraphics = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestProbedKittyGraphicsQueryFollowsTheDecision(t *testing.T) {
	// The state starts unknown and the query is only produced for a herdr
	// session, so ordinary terminals keep their environment detection.
	restore := SetProbedKittyGraphicsForTest(nil)
	defer restore()
	if query := ProbedKittyGraphicsQuery(); query != "" && !strings.Contains(query, "Gi=31") {
		t.Fatalf("probe query = %q", query)
	}
	if probed, known := ProbedKittyGraphics(); known || probed {
		t.Fatalf("probed state = (%v,%v), want unknown", probed, known)
	}
	accepted := true
	SetProbedKittyGraphics(&accepted)
	if probed, known := ProbedKittyGraphics(); !known || !probed {
		t.Fatalf("probed state = (%v,%v), want accepted", probed, known)
	}
}
