package tui

import (
	"fmt"
	"testing"

	"codex_go/shell"
)

// Rust #47417: distinguish Apple Terminal's native DA1/DA2 replies from every
// other terminal signature, in either arrival order.
func TestParseTerminalAppOverSSHDistinguishesNativeRepliesLikeRust(t *testing.T) {
	tests := []struct {
		primary   string
		secondary string
		want      bool
	}{
		{"1;2", "1;95;0", true},
		{"62;22;52", "1;10;0", false},
		{"64;1;2;4;6;17;18;21;22;52", "64;2500;0", false},
		{"62;52;", "1;4000;46", false},
		{"65;4;6;18;22", "1;277;0", false},
		{"6", "0;2600;1", false},
		{"1;2", "0;95;0", false},
		{"6", "1;95;0", false},
		{"1;2", "1;95;00", false},
		{"1;2", "1;95;1", false},
	}
	for _, testCase := range tests {
		primary := fmt.Sprintf("\x1b[?%sc", testCase.primary)
		secondary := fmt.Sprintf("\x1b[>%sc", testCase.secondary)
		for _, input := range []string{primary + secondary, secondary + primary} {
			got, known := ParseTerminalAppOverSSH([]byte(input))
			if !known {
				t.Fatalf("%q: identity should be known", input)
			}
			if got != testCase.want {
				t.Fatalf("%q: apple terminal = %v, want %v", input, got, testCase.want)
			}
		}
	}
}

// Rust #47417: unrelated CSI replies must not consume later typed text as
// device attributes.
func TestParseTerminalAppOverSSHIgnoresUnrelatedCSIRepliesLikeRust(t *testing.T) {
	for _, reply := range []string{"\x1b[?7u", "\x1b[>4;2m"} {
		input := []byte(reply + "c\x1b[>1;95;0c")
		if _, known := ParseTerminalAppOverSSH(input); known {
			t.Fatalf("%q: identity should be unknown", reply)
		}
		input = append(input, []byte("\x1b[?1;2c")...)
		got, known := ParseTerminalAppOverSSH(input)
		if !known || !got {
			t.Fatalf("%q: apple terminal = %v, known = %v", reply, got, known)
		}
	}
}

// Rust #47417: identity waits for both complete replies and ignores pasted
// lookalikes until the paste closes.
func TestParseTerminalAppOverSSHWaitsForCompleteRepliesLikeRust(t *testing.T) {
	signature := []byte("\x1b[?1;2c\x1b[>1;95;0c")
	for end := 0; end < len(signature); end++ {
		if _, known := ParseTerminalAppOverSSH(signature[:end]); known {
			t.Fatalf("prefix %q: identity should be unknown", signature[:end])
		}
	}
	input := append([]byte("typed\x1b[200~"), signature...)
	if _, known := ParseTerminalAppOverSSH(input); known {
		t.Fatal("pasted signature should not identify the terminal")
	}
	input = append(input, []byte("\x1b[201~more typed")...)
	if _, known := ParseTerminalAppOverSSH(input); known {
		t.Fatal("identity should remain unknown after the paste")
	}
	input = append(input, signature...)
	got, known := ParseTerminalAppOverSSH(input)
	if !known || !got {
		t.Fatalf("signature after paste: apple terminal = %v, known = %v", got, known)
	}
}

// Rust #47417: only an SSH session outside a multiplexer queries DA2.
func TestStartupProbeQueryTerminalIdentityLikeRust(t *testing.T) {
	originalInfo := startupProbeTerminalInfo
	originalEnv := startupProbeLookupEnv
	t.Cleanup(func() {
		startupProbeTerminalInfo = originalInfo
		startupProbeLookupEnv = originalEnv
	})

	setEnv := func(values map[string]string) {
		startupProbeLookupEnv = func(name string) (string, bool) {
			value, ok := values[name]
			return value, ok
		}
	}

	startupProbeTerminalInfo = func() *shell.TerminalInfo { return &shell.TerminalInfo{} }
	setEnv(map[string]string{})
	if StartupProbeQueryTerminalIdentity() {
		t.Fatal("local session should not query the terminal identity")
	}
	setEnv(map[string]string{"SSH_TTY": "/dev/ttys001"})
	if !StartupProbeQueryTerminalIdentity() {
		t.Fatal("SSH_TTY session should query the terminal identity")
	}
	setEnv(map[string]string{"SSH_CONNECTION": "10.0.0.1 1 10.0.0.2 2"})
	if !StartupProbeQueryTerminalIdentity() {
		t.Fatal("SSH_CONNECTION session should query the terminal identity")
	}
	startupProbeTerminalInfo = func() *shell.TerminalInfo {
		return &shell.TerminalInfo{Multiplexer: &shell.Multiplexer{Name: shell.MultiplexerTmux}}
	}
	if StartupProbeQueryTerminalIdentity() {
		t.Fatal("multiplexed SSH session should not query the terminal identity")
	}
}

// Rust #47417: the probe stays incomplete until both identity replies arrive
// even when keyboard detection is skipped.
func TestStartupProbeWaitsForTerminalIdentityLikeRust(t *testing.T) {
	probe := StartupProbe{}
	input := []byte("\x1b[1;1R\x1b]10;rgb:eeee/eeee/eeee\x07\x1b]11;rgb:1111/1111/1111\x07\x1b[?1;2c")
	UpdateStartupProbe(&probe, nil, input, StartupKeyboardEnhancementProbeSkip)
	if StartupProbeComplete(probe, StartupKeyboardEnhancementProbeSkip) {
		t.Fatal("probe completed without the identity reply")
	}
	input = append(input, []byte("\x1b[>1;95;0c")...)
	UpdateStartupProbe(&probe, nil, input, StartupKeyboardEnhancementProbeSkip)
	if probe.TerminalAppOverSSH == nil || !*probe.TerminalAppOverSSH {
		t.Fatalf("identity = %#v, want Apple Terminal", probe.TerminalAppOverSSH)
	}
	if !StartupProbeComplete(probe, StartupKeyboardEnhancementProbeSkip) {
		t.Fatal("probe should complete once the identity is known")
	}
}
