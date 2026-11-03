package tui

import (
	"os"
	"runtime"
	"testing"
)

// Rust parity: codex-rs/tui/src/terminal_palette.rs effective_stdout_color_level
// delegates to supports-color 3.0.2 (codex-rs/Cargo.toml pins that version), so
// the cases below are transcribed from that crate's supports_color(),
// env_force_color() and env_no_color() rather than from the NO_COLOR convention.
//
// Two things bit this repository three rounds in a row, so both are pinned:
//   - NO_COLOR is tested for *presence*. NO_COLOR="" disables colour and only
//     the literal "0" is ignored. The previous "empty NO_COLOR is unset" subtest
//     asserted the opposite of Rust and passed only because the host happened to
//     have WT_SESSION set.
//   - WT_SESSION promotion runs *after* the supports-color verdict and ignores
//     NO_COLOR completely (Rust then returns TrueColor), so every case that
//     asserts a NO_COLOR outcome must leave WT_SESSION absent.

func envFromMap(values map[string]string) envLookup {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

func TestSupportsColorLevelLikeRust(t *testing.T) {
	const quantumTerm = "xterm-256color"
	cases := []struct {
		name  string
		env   map[string]string
		noTTY bool
		want  StdoutColorLevel
	}{
		{
			name: "no_color_1_disables",
			env:  map[string]string{"TERM": quantumTerm, "NO_COLOR": "1"},
			want: ColorUnknown,
		},
		{
			name: "no_color_present_but_empty_disables",
			env:  map[string]string{"TERM": quantumTerm, "NO_COLOR": ""},
			want: ColorUnknown,
		},
		{
			name: "no_color_zero_does_not_disable",
			env:  map[string]string{"TERM": quantumTerm, "NO_COLOR": "0"},
			want: ColorANSI256,
		},
		{
			name: "no_color_other_value_disables",
			env:  map[string]string{"TERM": quantumTerm, "NO_COLOR": "no"},
			want: ColorUnknown,
		},
		{
			name: "force_color_3_beats_no_color",
			env:  map[string]string{"TERM": quantumTerm, "NO_COLOR": "1", "FORCE_COLOR": "3"},
			want: ColorTrue,
		},
		{
			name: "force_color_2_is_ansi256",
			env:  map[string]string{"TERM": "dumb", "FORCE_COLOR": "2"},
			want: ColorANSI256,
		},
		{
			name: "force_color_1_is_ansi16",
			env:  map[string]string{"TERM": "dumb", "FORCE_COLOR": "1"},
			want: ColorANSI16,
		},
		{
			name: "force_color_present_but_empty_is_ansi16",
			env:  map[string]string{"TERM": "dumb", "FORCE_COLOR": ""},
			want: ColorANSI16,
		},
		{
			name: "force_color_true_is_ansi16",
			env:  map[string]string{"TERM": "dumb", "FORCE_COLOR": "true"},
			want: ColorANSI16,
		},
		{
			name: "force_color_unparsable_falls_back_to_ansi16",
			env:  map[string]string{"TERM": "dumb", "FORCE_COLOR": "yes"},
			want: ColorANSI16,
		},
		{
			name: "force_color_caps_at_level_3",
			env:  map[string]string{"TERM": "dumb", "FORCE_COLOR": "9"},
			want: ColorTrue,
		},
		{
			// Rust does not treat FORCE_COLOR=0 as "disable"; it only cancels a
			// CLICOLOR_FORCE. The old Go test asserted Unknown here and passed
			// purely because `go test` stdout is not a tty.
			name: "force_color_zero_does_not_disable",
			env:  map[string]string{"TERM": quantumTerm, "FORCE_COLOR": "0"},
			want: ColorANSI256,
		},
		{
			name: "force_color_false_does_not_disable",
			env:  map[string]string{"TERM": quantumTerm, "FORCE_COLOR": "false"},
			want: ColorANSI256,
		},
		{
			name: "clicolor_force_one_is_ansi16",
			env:  map[string]string{"TERM": "dumb", "CLICOLOR_FORCE": "1"},
			want: ColorANSI16,
		},
		{
			name: "clicolor_force_zero_does_not_force",
			env:  map[string]string{"TERM": quantumTerm, "CLICOLOR_FORCE": "0"},
			want: ColorANSI256,
		},
		{
			name: "force_color_branch_shadows_clicolor_force",
			env:  map[string]string{"TERM": "dumb", "FORCE_COLOR": "0", "CLICOLOR_FORCE": "1"},
			want: ColorUnknown,
		},
		{
			name: "term_dumb_disables",
			env:  map[string]string{"TERM": "dumb"},
			want: ColorUnknown,
		},
		{
			name:  "not_a_tty_disables",
			env:   map[string]string{"TERM": quantumTerm},
			noTTY: true,
			want:  ColorUnknown,
		},
		{
			name:  "force_color_bypasses_tty_check",
			env:   map[string]string{"TERM": "dumb", "FORCE_COLOR": "3"},
			noTTY: true,
			want:  ColorTrue,
		},
		{
			name:  "ignore_is_terminal_restores_detection",
			env:   map[string]string{"TERM": quantumTerm, "IGNORE_IS_TERMINAL": "1"},
			noTTY: true,
			want:  ColorANSI256,
		},
		{
			name: "colorterm_truecolor_is_truecolor",
			env:  map[string]string{"TERM": "xterm", "COLORTERM": "truecolor"},
			want: ColorTrue,
		},
		{
			name: "colorterm_24bit_is_truecolor",
			env:  map[string]string{"TERM": "xterm", "COLORTERM": "24bit"},
			want: ColorTrue,
		},
		{
			name: "term_direct_suffix_is_truecolor",
			env:  map[string]string{"TERM": "xterm-direct"},
			want: ColorTrue,
		},
		{
			name: "term_256color_suffix_is_ansi256",
			env:  map[string]string{"TERM": "screen-256color"},
			want: ColorANSI256,
		},
		{
			name: "iterm_program_is_truecolor",
			env:  map[string]string{"TERM": "xterm", "TERM_PROGRAM": "iTerm.app"},
			want: ColorTrue,
		},
		{
			name: "apple_terminal_is_ansi256",
			env:  map[string]string{"TERM": "xterm", "TERM_PROGRAM": "Apple_Terminal"},
			want: ColorANSI256,
		},
		{
			name: "colorterm_alone_is_ansi16",
			env:  map[string]string{"TERM": "xterm", "COLORTERM": "maybe"},
			want: ColorANSI16,
		},

		// WT_SESSION / Windows Terminal promotion. Rust applies it after the
		// supports-color verdict and keys it off FORCE_COLOR *presence*.
		{
			name:  "wt_session_promotes_without_tty",
			env:   map[string]string{"WT_SESSION": "1"},
			noTTY: true,
			want:  ColorTrue,
		},
		{
			name:  "wt_session_present_but_empty_still_promotes",
			env:   map[string]string{"WT_SESSION": ""},
			noTTY: true,
			want:  ColorTrue,
		},
		{
			name:  "force_color_presence_blocks_wt_session_promotion",
			env:   map[string]string{"WT_SESSION": "1", "FORCE_COLOR": "1"},
			noTTY: true,
			want:  ColorANSI16,
		},
		{
			name:  "empty_force_color_blocks_wt_session_promotion",
			env:   map[string]string{"WT_SESSION": "1", "FORCE_COLOR": ""},
			noTTY: true,
			want:  ColorANSI16,
		},
		{
			name:  "force_color_zero_blocks_wt_session_promotion",
			env:   map[string]string{"WT_SESSION": "1", "FORCE_COLOR": "0"},
			noTTY: true,
			want:  ColorUnknown,
		},
		{
			// Rust quirk, mirrored on purpose: WT_SESSION outranks NO_COLOR.
			name:  "wt_session_promotion_ignores_no_color",
			env:   map[string]string{"WT_SESSION": "1", "NO_COLOR": "1"},
			noTTY: true,
			want:  ColorTrue,
		},
		{
			name: "windows_terminal_program_promotes_ansi16",
			env:  map[string]string{"TERM": "xterm", "TERM_PROGRAM": "Windows_Terminal"},
			want: ColorTrue,
		},
		{
			name: "windows_terminal_promotion_blocked_by_force_color_presence",
			env:  map[string]string{"TERM": "xterm", "TERM_PROGRAM": "Windows_Terminal", "FORCE_COLOR": "1"},
			want: ColorANSI16,
		},
		{
			name: "windows_terminal_promotion_only_upgrades_ansi16",
			env:  map[string]string{"TERM": quantumTerm, "TERM_PROGRAM": "Windows_Terminal"},
			want: ColorANSI256,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Every case declares its whole colour environment, so nothing the
			// host happens to export can leak in.
			if got := detectStdoutColorLevelFor(envFromMap(tc.env), !tc.noTTY); got != tc.want {
				t.Fatalf("detectStdoutColorLevelFor(%v, noTTY=%v) = %v, want %v", tc.env, tc.noTTY, got, tc.want)
			}
		})
	}
}

// TestAnsiColorForTermMatchesPlatformLikeRust pins supports-color's
// check_ansi_color(), whose "TERM unset" answer differs between platforms.
func TestAnsiColorForTermMatchesPlatformLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		if !ansiColorForTerm(envFromMap(map[string]string{})) {
			t.Fatalf("unset TERM on Windows should assume ANSI support")
		}
		if ansiColorForTerm(envFromMap(map[string]string{"TERM": "cygwin"})) {
			t.Fatalf("cygwin TERM should not be treated as ANSI capable")
		}
	} else if ansiColorForTerm(envFromMap(map[string]string{})) {
		t.Fatalf("unset TERM on Unix should not assume ANSI support")
	}
	if ansiColorForTerm(envFromMap(map[string]string{"TERM": "dumb"})) {
		t.Fatalf("TERM=dumb should never be ANSI capable")
	}
	if !ansiColorForTerm(envFromMap(map[string]string{"TERM": "xterm"})) {
		t.Fatalf("TERM=xterm should be ANSI capable")
	}
}

// colorEnvKeys is every variable detectStdoutColorLevel reads. Tests that go
// through the real process environment clear all of them first.
var colorEnvKeys = []string{
	"NO_COLOR",
	"FORCE_COLOR",
	"CLICOLOR_FORCE",
	"CLICOLOR",
	"WT_SESSION",
	"TERM",
	"COLORTERM",
	"TERM_PROGRAM",
	"IGNORE_IS_TERMINAL",
}

// unsetEnvForTest removes key for the duration of the test and restores the
// host value afterwards. t.Setenv cannot express "absent", and Rust's lookups
// distinguish absent from empty (NO_COLOR="" disables colour).
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	if previous, ok := os.LookupEnv(key); ok {
		t.Cleanup(func() { _ = os.Setenv(key, previous) })
	} else {
		t.Cleanup(func() { _ = os.Unsetenv(key) })
	}
	_ = os.Unsetenv(key)
}

// isolateColorEnvForTest removes every colour-related variable so a test only
// sees what it sets itself.
func isolateColorEnvForTest(t *testing.T) {
	t.Helper()
	for _, key := range colorEnvKeys {
		unsetEnvForTest(t, key)
	}
}

// TestDetectStdoutColorLevelRespectsProcessEnv exercises the real entry point
// (os.LookupEnv + os.Stdout stat) with an explicitly isolated environment.
func TestDetectStdoutColorLevelRespectsProcessEnv(t *testing.T) {
	isolateColorEnvForTest(t)
	t.Setenv("IGNORE_IS_TERMINAL", "1")

	t.Setenv("TERM", "xterm-256color")
	if got := detectStdoutColorLevel(); got != ColorANSI256 {
		t.Fatalf("base detection = %v, want ColorANSI256", got)
	}

	t.Setenv("NO_COLOR", "")
	if got := detectStdoutColorLevel(); got != ColorUnknown {
		t.Fatalf("NO_COLOR=\"\" = %v, want ColorUnknown (present counts)", got)
	}

	t.Setenv("NO_COLOR", "0")
	if got := detectStdoutColorLevel(); got != ColorANSI256 {
		t.Fatalf("NO_COLOR=0 = %v, want ColorANSI256", got)
	}

	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "3")
	if got := detectStdoutColorLevel(); got != ColorTrue {
		t.Fatalf("FORCE_COLOR=3 with NO_COLOR=1 = %v, want ColorTrue", got)
	}

	unsetEnvForTest(t, "NO_COLOR")
	unsetEnvForTest(t, "FORCE_COLOR")
	t.Setenv("WT_SESSION", "1")
	if got := detectStdoutColorLevel(); got != ColorTrue {
		t.Fatalf("WT_SESSION promotion = %v, want ColorTrue", got)
	}

	unsetEnvForTest(t, "WT_SESSION")
	t.Setenv("TERM", "dumb")
	if got := detectStdoutColorLevel(); got != ColorUnknown {
		t.Fatalf("TERM=dumb = %v, want ColorUnknown", got)
	}
}
