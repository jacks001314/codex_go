package cli

import "testing"

// TestCompletionShellDiagnosticsLikeRust pins clap's value parser for the
// optional [SHELL] positional of `codex completion`: an unknown value is a
// parse-time diagnostic with exit code 2, the alphabetical possible-value
// list, clap's did-you-mean tip and no usage block.
func TestCompletionShellDiagnosticsLikeRust(t *testing.T) {
	cases := []struct {
		args []string
		msg  string
	}{
		{
			[]string{"completion", "bogus"},
			"invalid value 'bogus' for '[SHELL]'\n  [possible values: bash, elvish, fish, powershell, zsh]",
		},
		{
			[]string{"completion", "bsh"},
			"invalid value 'bsh' for '[SHELL]'\n  [possible values: bash, elvish, fish, powershell, zsh]\n\n  tip: a similar value exists: 'bash'",
		},
		{
			[]string{"completion", "BASH"},
			"invalid value 'BASH' for '[SHELL]'\n  [possible values: bash, elvish, fish, powershell, zsh]",
		},
	}
	for _, tc := range cases {
		_, err := Parse(tc.args)
		usageErr, ok := err.(*UsageError)
		if !ok {
			t.Fatalf("%v: error = %v, want clap diagnostic", tc.args, err)
		}
		if usageErr.Message != tc.msg {
			t.Fatalf("%v: message = %q, want %q", tc.args, usageErr.Message, tc.msg)
		}
		if usageErr.Usage != "" {
			t.Fatalf("%v: usage = %q, want none", tc.args, usageErr.Usage)
		}
	}
}
