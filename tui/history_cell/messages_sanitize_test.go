package historycell

import "testing"

// Rust parity: codex-rs/tui/src/history_cell/messages.rs::sanitize_user_text.
func TestSanitizeUserTextMatchesRustCore(t *testing.T) {
	for _, testCase := range []struct {
		in   string
		want string
	}{
		{in: "plain text", want: "plain text"},
		{in: "line\nbreak\ttab", want: "line\nbreak\ttab"},
		{in: "bell\x07null\x00", want: "bellnull"},
		{in: "\x1b[31mred\x1b[0m", want: "red"},
		{in: "esc\x1bnot csi", want: "escnot csi"},
		{in: "truncated\x1b[31", want: "truncated"},
		{in: "multi\x1b[38;5;200mcolour\x1b[0m", want: "multicolour"},
	} {
		if got := SanitizeUserText(testCase.in); got != testCase.want {
			t.Fatalf("SanitizeUserText(%q) = %q, want %q", testCase.in, got, testCase.want)
		}
	}
}
