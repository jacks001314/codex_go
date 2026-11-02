package tui

import "testing"

// Rust #46897: effective_stdout_color_level delegates to supports-color, which
// honours NO_COLOR (empty means unset) and FORCE_COLOR. The activity chart keys
// its hollow/filled glyph pair off that level, so NO_COLOR must not look like a
// 256-colour terminal.
func TestDetectStdoutColorLevelHonorsNoColorLikeRust(t *testing.T) {
	cases := []struct {
		name       string
		term       string
		noColor    string
		forceColor string
		want       StdoutColorLevel
	}{
		{name: "nocolor wins over 256color term", term: "xterm-256color", noColor: "1", want: ColorUnknown},
		{name: "empty NO_COLOR is unset", term: "dumb", noColor: "", want: ColorUnknown},
		{name: "force color 3 beats NO_COLOR", term: "dumb", noColor: "1", forceColor: "3", want: ColorTrue},
		{name: "force color 0 disables", term: "xterm-256color", forceColor: "0", want: ColorUnknown},
		{name: "force color 1 is ansi16", term: "dumb", forceColor: "1", want: ColorANSI16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM", tc.term)
			t.Setenv("NO_COLOR", tc.noColor)
			t.Setenv("FORCE_COLOR", tc.forceColor)
			if got := detectStdoutColorLevel(); got != tc.want {
				t.Fatalf("detectStdoutColorLevel() = %v, want %v", got, tc.want)
			}
		})
	}
}
