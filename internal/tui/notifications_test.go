package tui

import "testing"

func TestNotificationSequenceOSC9AndBEL(t *testing.T) {
	if got := NotificationSequence(NotificationMethodOSC9, "hello", NotificationSequenceOptions{}); got != "\x1b]9;hello\x07" {
		t.Fatalf("OSC9 sequence = %q", got)
	}
	if got := NotificationSequence(NotificationMethodBEL, "hello", NotificationSequenceOptions{}); got != "\x07" {
		t.Fatalf("BEL sequence = %q", got)
	}
	if got := NotificationSequence(NotificationMethodOSC9, "danger\x1b[31m", NotificationSequenceOptions{Tmux: true}); got != "\x1bPtmux;\x1b\x1b]9;danger\x1b\x1b[31m\x07\x1b\\" {
		t.Fatalf("tmux OSC9 sequence = %q", got)
	}
}

func TestNotificationAutoAndCondition(t *testing.T) {
	if got := NotificationSequence(NotificationMethodAuto, "done", NotificationSequenceOptions{SupportsOSC9: true}); got != "\x1b]9;done\x07" {
		t.Fatalf("auto OSC9 sequence = %q", got)
	}
	if got := NotificationSequence(NotificationMethodAuto, "done", NotificationSequenceOptions{}); got != "\x07" {
		t.Fatalf("auto BEL sequence = %q", got)
	}
	if ShouldEmitNotification(NotificationConditionUnfocused, true) {
		t.Fatal("unfocused condition emitted while focused")
	}
	if !ShouldEmitNotification(NotificationConditionUnfocused, false) {
		t.Fatal("unfocused condition did not emit while blurred")
	}
	if !ShouldEmitNotification(NotificationConditionAlways, true) {
		t.Fatal("always condition did not emit while focused")
	}
}

func TestNotificationEnvSupportsOSC9(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{name: "wezterm term program", env: map[string]string{"TERM_PROGRAM": "WezTerm"}, want: true},
		{name: "iterm app term program", env: map[string]string{"TERM_PROGRAM": "iTerm.app"}, want: true},
		{name: "warp term program", env: map[string]string{"TERM_PROGRAM": "WarpTerminal"}, want: true},
		{name: "windows terminal unsupported", env: map[string]string{"TERM_PROGRAM": "WindowsTerminal"}, want: false},
		{name: "term program masks later probes", env: map[string]string{"TERM_PROGRAM": "WindowsTerminal", "WEZTERM_VERSION": "20240203"}, want: false},
		{name: "wezterm env", env: map[string]string{"WEZTERM_VERSION": "20240203"}, want: true},
		{name: "iterm env", env: map[string]string{"ITERM_SESSION_ID": "w0t0p0"}, want: true},
		{name: "kitty env", env: map[string]string{"KITTY_WINDOW_ID": "1"}, want: true},
		{name: "kitty term", env: map[string]string{"TERM": "xterm-kitty"}, want: true},
		{name: "wezterm term", env: map[string]string{"TERM": "wezterm-mux"}, want: true},
		{name: "unknown", env: map[string]string{"TERM": "xterm-256color"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := func(key string) string {
				return tt.env[key]
			}
			if got := NotificationEnvSupportsOSC9(env); got != tt.want {
				t.Fatalf("NotificationEnvSupportsOSC9() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTerminalProgramSupportsOSC9MatchesRustNormalization(t *testing.T) {
	for _, value := range []string{"Ghostty", "iTerm2", "iTerm.app", "kitty", "Warp-Terminal", "Wez_Term"} {
		if !terminalProgramSupportsOSC9(value) {
			t.Fatalf("%q should support OSC9", value)
		}
	}
	for _, value := range []string{"WindowsTerminal", "VS Code", "mykitty"} {
		if terminalProgramSupportsOSC9(value) {
			t.Fatalf("%q should not support OSC9", value)
		}
	}
}
