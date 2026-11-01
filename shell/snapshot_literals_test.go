package shell

import (
	"slices"
	"strings"
	"testing"
)

// TestSnapshotLiteralWordsDecodeShellQuotingLikeRust pins the literal decoder
// that credential policy uses for snapshot scripts
// (shell-command/src/shell_snapshot_literals.rs): a secret an alias or function
// body rebuilds from quoted fragments must surface as one decoded word.
func TestSnapshotLiteralWordsDecodeShellQuotingLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		shell   ShellType
		script  string
		contain []string
		absent  []string
	}{
		{
			name:    "single quoted value",
			shell:   ShellBash,
			script:  "export TOKEN='sk-abc'\n",
			contain: []string{"export", "TOKEN=sk-abc"},
		},
		{
			name:    "double quoted value",
			shell:   ShellBash,
			script:  "export TOKEN=\"sk-abc\"\n",
			contain: []string{"TOKEN=sk-abc"},
		},
		{
			name:    "double quote escapes",
			shell:   ShellBash,
			script:  "echo \"a\\\"b$c\\\\d\\ne\"\n",
			contain: []string{"a\"b$c\\d\\ne"},
		},
		{
			name:    "backslash outside quotes",
			shell:   ShellBash,
			script:  "printf %s token_ab\\'cdefghijklmnop\n",
			contain: []string{"token_ab'cdefghijklmnop"},
		},
		{
			name:    "escaped single quote concatenation",
			shell:   ShellBash,
			script:  "alias helper='printf %s token_ab'\\''cdef'\n",
			contain: []string{"helper=printf %s token_ab'cdef"},
		},
		{
			name:    "ansi c quoting",
			shell:   ShellBash,
			script:  "token=$'sk-\\x61\\142c'\n",
			contain: []string{"token=sk-abc"},
		},
		{
			name:    "ansi c unknown escape keeps backslash in bash",
			shell:   ShellBash,
			script:  "token=$'a\\qc'\n",
			contain: []string{"token=a\\qc"},
		},
		{
			name:    "ansi c unknown escape drops backslash in zsh",
			shell:   ShellZsh,
			script:  "token=$'a\\qc'\n",
			contain: []string{"token=aqc"},
		},
		{
			name:    "backtick body is its own script",
			shell:   ShellBash,
			script:  "value=`printf '%s' 'sk-abc'`\n",
			contain: []string{"value=", "sk-abc"},
		},
		{
			name:    "command substitution body is its own script",
			shell:   ShellBash,
			script:  "value=$(printf \"%s\" 'sk-abc')\n",
			contain: []string{"value=", "sk-abc"},
		},
		{
			name:    "comment lines are skipped",
			shell:   ShellBash,
			script:  "# Snapshot file\n# setopts 1\nexport A='b#c'\n",
			contain: []string{"export", "A=b#c"},
			absent:  []string{"Snapshot", "setopts"},
		},
		{
			name:    "function body",
			shell:   ShellZsh,
			script:  "helper () {\n\tprintf '%s' 'sk-abc'\n}\n",
			contain: []string{"helper", "sk-abc"},
		},
		{
			name:    "line continuation joins fragments",
			shell:   ShellBash,
			script:  "token=sk-\\\nabc\n",
			contain: []string{"token=sk-abc"},
		},
		{
			name:    "bash backed sh snapshot scans its array initializer",
			shell:   ShellSh,
			script:  bashShSnapshotHeader + "array=('sk-' 'abc')\n",
			contain: []string{"array=", "sk-"},
		},
		{
			name:   "unsupported shell has no literal scan",
			shell:  ShellFish,
			script: "export TOKEN='sk-abc'\n",
			absent: []string{"TOKEN=sk-abc"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			words := SnapshotLiteralWords(testCase.shell, testCase.script)
			for _, want := range testCase.contain {
				if !slices.Contains(words, want) {
					t.Fatalf("SnapshotLiteralWords(%q) = %#v, want %q", testCase.script, words, want)
				}
			}
			for _, unwanted := range testCase.absent {
				for _, word := range words {
					if strings.Contains(word, unwanted) {
						t.Fatalf("SnapshotLiteralWords(%q) = %#v, did not want %q", testCase.script, words, unwanted)
					}
				}
			}
		})
	}
}