package tui

import (
	"testing"

	"codex_go/shell"
)

func TestTerminalProgramStatusProtocolsLikeRust(t *testing.T) {
	directITerm := &shell.TerminalInfo{Name: shell.TerminalIterm2}
	tests := []struct {
		name           string
		terminal       *shell.TerminalInfo
		stdoutTerminal bool
		screenActive   bool
		want           terminalProgramStatusSupport
	}{
		{
			name:           "direct iTerm gets both protocols",
			terminal:       directITerm,
			stdoutTerminal: true,
			want:           terminalProgramStatusSupport{osc7501: true, osc21337: true},
		},
		{
			name:           "non terminal emits nothing",
			terminal:       directITerm,
			stdoutTerminal: false,
			want:           terminalProgramStatusSupport{},
		},
		{
			name:           "multiplexed iTerm gets generic status",
			terminal:       &shell.TerminalInfo{Name: shell.TerminalIterm2, Multiplexer: &shell.Multiplexer{Name: shell.MultiplexerTmux}},
			stdoutTerminal: true,
			want:           terminalProgramStatusSupport{osc7501: true},
		},
		{
			name:           "other terminal gets generic status",
			terminal:       &shell.TerminalInfo{Name: shell.TerminalGhostty},
			stdoutTerminal: true,
			want:           terminalProgramStatusSupport{osc7501: true},
		},
		{
			name:           "GNU screen gets generic status",
			terminal:       directITerm,
			stdoutTerminal: true,
			screenActive:   true,
			want:           terminalProgramStatusSupport{osc7501: true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := terminalProgramStatusSupportFor(tc.terminal, tc.stdoutTerminal, tc.screenActive)
			if got != tc.want {
				t.Fatalf("protocol support = %#v, want %#v", got, tc.want)
			}
		})
	}
}
