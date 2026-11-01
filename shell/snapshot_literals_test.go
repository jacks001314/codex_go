package shell

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
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

// TestSnapshotLiteralWordsBoundsNestedSubstitutionsLikeRust keeps a
// pathological snapshot from exhausting the stack: nesting beyond the bound is
// left to the raw text check instead of being parsed recursively.
func TestSnapshotLiteralWordsBoundsNestedSubstitutionsLikeRust(t *testing.T) {
	shallow := "$($('sk-abc'))\n"
	if words := SnapshotLiteralWords(ShellBash, shallow); !slices.Contains(words, "sk-abc") {
		t.Fatalf("shallow substitution words = %#v", words)
	}
	deep := strings.Repeat("$(", maxSnapshotLiteralNesting+8) + "'sk-abc'" + strings.Repeat(")", maxSnapshotLiteralNesting+8)
	if words := SnapshotLiteralWords(ShellBash, deep); slices.Contains(words, "sk-abc") {
		t.Fatalf("deep substitution was parsed past the bound: %#v", words)
	}
}

// TestSnapshotLiteralWordsMatchRealBashAliasOutputLikeRust runs Rust's
// brokered_snapshots_check_complete_alias_literals differential: a real bash
// quotes the alias value, the literal scanner decodes it, and the decoded word
// must reproduce the original value exactly.
func TestSnapshotLiteralWordsMatchRealBashAliasOutputLikeRust(t *testing.T) {
	bashPath := snapshotTestBashPath(t)
	if bashPath == "" {
		t.Skip("no bash available for the snapshot literal differential")
	}
	for _, value := range []string{
		"token_ab'cdefghijklmnop",
		"plain value",
		"line1\nline2",
		"tab\there",
		"control\x01char",
		"back\\slash and $dollar",
		"quote\"inside",
		"",
	} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			command := exec.Command(bashPath, "-c", `alias helper="$ALIAS_VALUE"; alias -p`)
			command.Env = []string{"ALIAS_VALUE=" + value}
			output, err := command.Output()
			if err != nil {
				t.Fatalf("bash alias probe failed: %v", err)
			}
			text := "# aliases\n" + string(output) + "\n# exports 0\n"
			if words := SnapshotLiteralWords(ShellBash, text); !slices.Contains(words, "helper="+value) {
				t.Fatalf("SnapshotLiteralWords(%q) = %#v, want the alias value %q", text, words, value)
			}
		})
	}
}

// TestSnapshotLiteralWordsMatchRealZshAliasOutputLikeRust is the zsh half of
// Rust's alias differential. zsh may print an alias value differently from the
// value it replays (RCS quoting, $'...' escapes), so the expected value comes
// from replaying the captured alias line in zsh itself. The test skips when no
// zsh is installed, and then runs wherever a POSIX zsh is available.
func TestSnapshotLiteralWordsMatchRealZshAliasOutputLikeRust(t *testing.T) {
	zshPath := snapshotTestZshPath()
	if zshPath == "" {
		t.Skip("no zsh available for the snapshot literal differential")
	}
	for _, value := range []string{
		"token_ab'cdefghijklmnop",
		"plain value",
		"line1\nline2",
		"tab\there",
		"back\\slash and $dollar",
		"quote\"inside",
	} {
		t.Run(strconv.Quote(value), func(t *testing.T) {
			capture := exec.Command(zshPath, "-fc", `alias helper="$ALIAS_VALUE"; alias -L`)
			capture.Env = []string{"ALIAS_VALUE=" + value}
			output, err := capture.Output()
			if err != nil {
				t.Fatalf("zsh alias probe failed: %v", err)
			}
			text := "# aliases\n" + string(output) + "\n# exports 0\n"
			replay := exec.Command(zshPath, "-fc", text+"print -rn -- $aliases[helper]")
			replay.Env = []string{}
			expected, err := replay.Output()
			if err != nil {
				t.Fatalf("zsh alias replay failed: %v\n%s", err, text)
			}
			if words := SnapshotLiteralWords(ShellZsh, text); !slices.Contains(words, "helper="+string(expected)) {
				t.Fatalf("SnapshotLiteralWords(%q) = %#v, want the alias value %q", text, words, string(expected))
			}
		})
	}
}

// snapshotTestZshPath finds a POSIX zsh for the differential; the test skips
// without one.
func snapshotTestZshPath() string {
	for _, candidate := range []string{
		`C:\msys64\usr\bin\zsh.exe`,
		`C:\Program Files\Git\usr\bin\zsh.exe`,
		"/bin/zsh",
		"/usr/bin/zsh",
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if path, err := exec.LookPath("zsh"); err == nil {
		return path
	}
	return ""
}

// TestSnapshotLiteralWordsDecodeHeredocsLikeRust pins Rust's heredoc handling:
// the body is decoded as heredoc text instead of being parsed as script, so an
// unbalanced quote inside it cannot swallow the rest of the snapshot, and an
// unquoted delimiter removes one escape layer.
func TestSnapshotLiteralWordsDecodeHeredocsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		shell   ShellType
		script  string
		contain []string
		absent  []string
	}{
		{
			name:    "quoted heredoc keeps an unbalanced quote from leaking",
			shell:   ShellBash,
			script:  "cat <<'EOF'\nit's a quote\nEOF\nexport TOKEN='sk-abc'\n",
			contain: []string{"it's a quote", "TOKEN=sk-abc"},
		},
		{
			name:    "quoted heredoc keeps backslashes",
			shell:   ShellBash,
			script:  "cat <<'EOF'\nsk-re\\al\nEOF\n",
			contain: []string{"sk-re\\al"},
		},
		{
			name:    "unquoted heredoc removes one escape layer",
			shell:   ShellBash,
			script:  "cat <<EOF\nsk-re\\\nalsecret\nEOF\n",
			contain: []string{"sk-realsecret"},
		},
		{
			name:    "strip-tabs heredoc finds its terminator",
			shell:   ShellBash,
			script:  "cat <<-EOF\n\tsk-abc\n\tEOF\nexport TOKEN='after'\n",
			contain: []string{"sk-abc", "TOKEN=after"},
		},
		{
			name:    "double quoted delimiter counts as quoted",
			shell:   ShellZsh,
			script:  "cat <<\"EOF\"\nsk-re\\al\nEOF\n",
			contain: []string{"sk-re\\al"},
			absent:  []string{"sk-realsecret"},
		},
		{
			name:    "unterminated heredoc consumes the rest",
			shell:   ShellBash,
			script:  "cat <<EOF\nsk-abc\n",
			contain: []string{"sk-abc"},
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

// TestSnapshotLiteralWordsFollowRcQuotesLikeRust covers zsh rcquotes: after
// "setopt rcquotes" a doubled single quote is one literal quote, so an alias
// value that contains a quote decodes with it instead of losing it.
func TestSnapshotLiteralWordsFollowRcQuotesLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		shell   ShellType
		script  string
		contain []string
		absent  []string
	}{
		{
			name:    "rcquotes keeps the doubled quote",
			shell:   ShellZsh,
			script:  "setopt rcquotes\nalias helper='a''b'\n",
			contain: []string{"helper=a'b"},
			absent:  []string{"helper=ab"},
		},
		{
			name:    "rcquotes reassembles a quoted credential",
			shell:   ShellZsh,
			script:  "setopt rcquotes\nalias helper='sk-real''secret'\n",
			contain: []string{"helper=sk-real'secret"},
		},
		{
			name:    "default zsh concatenates the empty string",
			shell:   ShellZsh,
			script:  "alias helper='a''b'\n",
			contain: []string{"helper=ab"},
		},
		{
			name:    "unsetopt restores the old quoting",
			shell:   ShellZsh,
			script:  "setopt rcquotes\nunsetopt rcquotes\nalias helper='a''b'\n",
			contain: []string{"helper=ab"},
		},
		{
			name:    "bash ignores the zsh option",
			shell:   ShellBash,
			script:  "setopt rcquotes\nalias helper='a''b'\n",
			contain: []string{"helper=ab"},
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
