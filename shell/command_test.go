package shell

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDetectShellType(t *testing.T) {
	cases := map[string]ShellType{
		"zsh":            ShellZsh,
		"/bin/bash":      ShellBash,
		"pwsh":           ShellPowerShell,
		"powershell.exe": ShellPowerShell,
		"C:/Windows/cmd": ShellCmd,
		"/usr/bin/other": "",
	}
	for input, want := range cases {
		got, ok := DetectShellType(input)
		if want == "" {
			if ok {
				t.Fatalf("DetectShellType(%q) ok = true", input)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("DetectShellType(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
}

func TestTokenizePowerShellCommandPreservesWindowsPaths(t *testing.T) {
	for _, test := range []struct {
		command string
		want    []string
	}{
		{command: `Get-Content C:\skills\demo\SKILL.md`, want: []string{"Get-Content", "C:/skills/demo/SKILL.md"}},
		{command: `get-content -Raw "C:\skills and plugins\SKILL.md"`, want: []string{"Get-Content", "-Raw", "C:/skills and plugins/SKILL.md"}},
		{command: `Get-Content 'C:\skills and plugins\SKILL.md'`, want: []string{"Get-Content", "C:/skills and plugins/SKILL.md"}},
		{command: `gc C:/skills/demo/SKILL.md`, want: []string{"Get-Content", "C:/skills/demo/SKILL.md"}},
		{command: `type C:/skills/demo/SKILL.md`, want: []string{"Get-Content", "C:/skills/demo/SKILL.md"}},
		{command: `Get-Content C:\skills\demo\SKILL.md -Raw`, want: []string{"Get-Content", "C:/skills/demo/SKILL.md", "-Raw"}},
		{command: `Get-Content -Raw -LiteralPath C:\skills\demo\SKILL.md`, want: []string{"Get-Content", "-Raw", "-LiteralPath", "C:/skills/demo/SKILL.md"}},
	} {
		got := TokenizePowerShellCommand(test.command)
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("TokenizePowerShellCommand(%q) = %#v, want %#v", test.command, got, test.want)
		}
	}
}

func TestReadPathsRecognizesPowerShellGetContentForms(t *testing.T) {
	for _, test := range []struct {
		command string
		want    string
	}{
		{command: `Get-Content C:/skills/demo/SKILL.md`, want: "C:/skills/demo/SKILL.md"},
		{command: `Get-Content -Raw C:/skills/demo/SKILL.md`, want: "C:/skills/demo/SKILL.md"},
		{command: `Get-Content -Path C:/skills/demo/SKILL.md`, want: "C:/skills/demo/SKILL.md"},
		{command: `Get-Content -LiteralPath C:/skills/demo/SKILL.md`, want: "C:/skills/demo/SKILL.md"},
		{command: `Get-Content C:/skills/demo/SKILL.md -Raw`, want: "C:/skills/demo/SKILL.md"},
		{command: `Get-Content -Raw -LiteralPath C:/skills/demo/SKILL.md`, want: "C:/skills/demo/SKILL.md"},
		{command: `gc C:/skills/demo/SKILL.md`, want: "C:/skills/demo/SKILL.md"},
		{command: `type C:/skills/demo/SKILL.md`, want: "C:/skills/demo/SKILL.md"},
	} {
		got := ReadPaths(TokenizePowerShellCommand(test.command))
		if !reflect.DeepEqual(got, []string{test.want}) {
			t.Fatalf("ReadPaths(TokenizePowerShellCommand(%q)) = %#v, want %#v", test.command, got, []string{test.want})
		}
	}
	for _, command := range []string{
		`Get-Content 'C:\Users\O''Brien\skill\SKILL.md'`,
		`Get-Content "$(Remove-Item C:/important)/skills/demo/SKILL.md"`,
		`Get-Content -ReadCount:([IO.File]::Delete('C:/important')) C:/skills/demo/SKILL.md`,
		`Get-Content C:/Users/Alice/.ssh/id_rsa,C:/skills/demo/SKILL.md`,
		`Get-Content C:/skills/demo/SKILL.md -Raw; Remove-Item C:/important`,
		`Get-Content C:/skills/demo/SKILL.md C:/important`,
		`Get-Content C:/skills/*/SKILL.md`,
		`Get-Content -Encoding UTF8 C:/skills/demo/SKILL.md`,
		`Get-Content -Raw`,
	} {
		if got := ReadPaths(TokenizePowerShellCommand(command)); len(got) != 0 {
			t.Fatalf("ReadPaths(TokenizePowerShellCommand(%q)) = %#v, want none", command, got)
		}
	}
}

func TestShlexJoin(t *testing.T) {
	got := ShlexJoin([]string{"echo", "hello world", "it's"})
	if got != `echo 'hello world' 'it'\''s'` {
		t.Fatalf("ShlexJoin() = %q", got)
	}
}

func TestStripShellCommandAndEscapeMatchesRustDisplay(t *testing.T) {
	cases := []struct {
		name    string
		command []string
		want    string
	}{
		{
			name:    "bash login command",
			command: []string{"bash", "-lc", "echo hello"},
			want:    "echo hello",
		},
		{
			name:    "zsh command flag",
			command: []string{"/usr/bin/zsh", "-c", "echo hello"},
			want:    "echo hello",
		},
		{
			name:    "powershell command with profile flags",
			command: []string{"pwsh", "-NoProfile", "-Command", "Get-ChildItem"},
			want:    "Get-ChildItem",
		},
		{
			name:    "fish is not stripped in rust tui display helper",
			command: []string{"fish", "-lc", "echo hello"},
			want:    "fish -lc 'echo hello'",
		},
		{
			name:    "fallback shell quoting",
			command: []string{"foo", "bar baz", "weird&stuff"},
			want:    "foo 'bar baz' 'weird&stuff'",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripShellCommandAndEscape(tc.command); got != tc.want {
				t.Fatalf("StripShellCommandAndEscape() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPowerShellExtractionAndPrefix(t *testing.T) {
	command := []string{"pwsh", "-NoProfile", "-Command", "Write-Host hi"}
	_, script, ok := ExtractPowerShellCommand(command)
	if !ok || script != "Write-Host hi" {
		t.Fatalf("ExtractPowerShellCommand() = %q, %v", script, ok)
	}
	prefixed := PrefixPowerShellScriptWithUTF8(command)
	want := []string{"pwsh", "-NoProfile", "-Command", UTF8OutputPrefix + "Write-Host hi"}
	if !reflect.DeepEqual(prefixed, want) {
		t.Fatalf("PrefixPowerShellScriptWithUTF8() = %#v", prefixed)
	}
	if !reflect.DeepEqual(PrefixPowerShellScriptWithUTF8(prefixed), want) {
		t.Fatalf("prefix duplicated")
	}
}

func TestParseCommandUnknown(t *testing.T) {
	parsed := ParseCommand([]string{"git", "status"})
	if len(parsed) != 1 || parsed[0].Kind != "unknown" {
		t.Fatalf("ParseCommand() = %+v", parsed)
	}
}

func TestSplitCommandLineMatchesRustShlexFallback(t *testing.T) {
	got := SplitCommandLine(`python3 -u "scripts/fetch comments.py"`)
	want := []string{"python3", "-u", "scripts/fetch comments.py"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitCommandLine() = %#v, want %#v", got, want)
	}
	got = SplitCommandLine(`cat "unterminated`)
	want = []string{"cat", `"unterminated`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SplitCommandLine(malformed) = %#v, want %#v", got, want)
	}
}

func TestReadPathsMatchesRustImplicitSkillFixtures(t *testing.T) {
	tests := []struct {
		command string
		want    string
	}{
		{command: `cat /tmp/skill-test/SKILL.md | head`, want: "/tmp/skill-test/SKILL.md"},
		{command: `bash -lc "cat SKILL.md"`, want: "SKILL.md"},
		{command: `bat --theme TwoDark SKILL.md`, want: "SKILL.md"},
		{command: `batcat SKILL.md`, want: "SKILL.md"},
		{command: `less -p TODO SKILL.md`, want: "SKILL.md"},
		{command: `more SKILL.md`, want: "SKILL.md"},
		{command: `head -n 50 SKILL.md`, want: "SKILL.md"},
		{command: `head -n50 SKILL.md`, want: "SKILL.md"},
		{command: `tail -n +10 SKILL.md`, want: "SKILL.md"},
		{command: `tail -n+10 SKILL.md`, want: "SKILL.md"},
		{command: `awk '{print $1}' SKILL.md`, want: "SKILL.md"},
		{command: `nl -ba SKILL.md`, want: "SKILL.md"},
		{command: `sed -n '12,20p' SKILL.md`, want: "SKILL.md"},
		{command: `cat -- -SKILL.md`, want: "-SKILL.md"},
		{command: `cd dir1 dir2 && cat SKILL.md`, want: filepath.Join("dir2", "SKILL.md")},
		{command: `cd -- -weird && cat SKILL.md`, want: filepath.Join("-weird", "SKILL.md")},
	}
	for _, test := range tests {
		t.Run(test.command, func(t *testing.T) {
			got := ReadPaths(SplitCommandLine(test.command))
			if !reflect.DeepEqual(got, []string{test.want}) {
				t.Fatalf("ReadPaths() = %#v, want %#v", got, []string{test.want})
			}
		})
	}

	for _, command := range []string{
		`cat first.md second.md`,
		`head -n 40`,
		`tail -c 30 SKILL.md`,
		`awk '{print $1}'`,
		`sed -n +10p SKILL.md`,
	} {
		t.Run("reject "+command, func(t *testing.T) {
			if got := ReadPaths(SplitCommandLine(command)); len(got) != 0 {
				t.Fatalf("ReadPaths() = %#v, want none", got)
			}
		})
	}
}
