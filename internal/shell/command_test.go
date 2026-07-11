package shell

import (
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
