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
