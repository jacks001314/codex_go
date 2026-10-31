package execpolicy

import "testing"

// Mirrors codex-rs/shell-command/src/command_safety/is_dangerous_command.rs
// (#42113): forced rm variants are dangerous across wrappers.
func TestDangerousCommandMatchForcedRmLikeRust(t *testing.T) {
	cases := [][]string{
		{"rm", "-rf", "/"},
		{"rm", "-f", "/"},
		{"/bin/rm", "-fr", "/tmp/example"},
		{"rm", "-r", "-f", "/tmp/example"},
		{"rm", "--force", "/tmp/example"},
		{"rm", "/tmp/example", "-f"},
		{"sudo", "rm", "-rf", "/tmp/example"},
		{"env", "TARGET=/tmp/example", "rm", "-rf", "/tmp/example"},
	}
	for _, command := range cases {
		match, ok := MatchDangerousCommand(command)
		if !ok || match != DangerousCommandMatchForcedRm {
			t.Fatalf("MatchDangerousCommand(%v) = %q, %v, want forced rm", command, match, ok)
		}
	}
}

func TestDangerousCommandMatchDeeplyNestedWrappersFailClosedLikeRust(t *testing.T) {
	for _, test := range []struct {
		depth int
		want  DangerousCommandMatch
	}{
		{MaxDangerousCommandWrapperDepth, DangerousCommandMatchForcedRm},
		{MaxDangerousCommandWrapperDepth + 1, DangerousCommandMatchOther},
	} {
		command := make([]string, 0, test.depth+3)
		for index := 0; index < test.depth; index++ {
			command = append(command, "env")
		}
		command = append(command, "rm", "-rf", "/tmp/example")
		match, ok := MatchDangerousCommand(command)
		if !ok || match != test.want {
			t.Fatalf("depth %d: match = %q, %v, want %q", test.depth, match, ok, test.want)
		}
	}
}

func TestDangerousCommandMatchComplexShellSyntaxLikeRust(t *testing.T) {
	scripts := []string{
		"printf x | rm -rf /tmp/example",
		"if test -d /tmp/example; then rm --force /tmp/example; fi",
		`rm -rf "$TARGET" >/dev/null`,
		`for target in /tmp/a /tmp/b; do rm -r -f "$target"; done`,
		`echo "$(rm -rf /tmp/example)"`,
		"bash -c 'rm -rf /tmp/example'",
		"trap 'rm -rf /tmp/example' EXIT",
		`for a in '-C5a25KeRr' '--' '--json' '--bogus'; do HOME=$(mktemp -d) MDE_URL=http://127.0.0.1:1 MDE_TOKEN=x node cli/mde.cjs ls "$a" >/tmp/mde-review-out 2>/tmp/mde-review-err; code=$?; printf '%s\t%s\t%s\n' "$a" "$code" "$(tr '\n' ' ' </tmp/mde-review-err)"; rm -rf "$HOME"; done`,
	}
	for _, script := range scripts {
		match, ok := MatchDangerousCommand([]string{"bash", "-lc", script})
		if !ok || match != DangerousCommandMatchForcedRm {
			t.Fatalf("script %q: match = %q, %v, want forced rm", script, match, ok)
		}
	}
}

func TestDangerousCommandMatchIgnoresQuotedOrDynamicRmLikeRust(t *testing.T) {
	cases := [][]string{
		{"rm", "-r", "/tmp/example"},
		{"rm", "--", "-f"},
		{"bash", "-lc", "echo 'rm -rf /tmp/example'"},
		{"bash", "-lc", "cmd=rm; $cmd -rf /tmp/example"},
		{"bash", "-lc", "if then rm -rf /tmp/example"},
		{"env", "TARGET=/tmp/example", "rm", "-r", "/tmp/example"},
		{"bash", "-lc", "trap 'echo rm -rf /tmp/example' EXIT"},
	}
	for _, command := range cases {
		if match, ok := MatchDangerousCommand(command); ok {
			t.Fatalf("MatchDangerousCommand(%v) = %q, want no match", command, match)
		}
	}
}

func TestDangerousPowerShellWordsMatchFollowsPlatformLikeRust(t *testing.T) {
	words := []string{"Remove-Item", "test", "-Force"}
	if match, ok := MatchDangerousPowerShellWordsForPlatform(words, DangerousCommandPlatformWindows); !ok || match != DangerousCommandMatchOther {
		t.Fatalf("windows match = %q, %v, want other", match, ok)
	}
	if match, ok := MatchDangerousPowerShellWordsForPlatform(words, DangerousCommandPlatformPosix); ok {
		t.Fatalf("posix match = %q, want no match", match)
	}
}

// Mirrors Rust #42113's Windows dangerous-command rules. These run on every
// host because the platform is explicit.
func TestDangerousCommandMatchWindowsRulesLikeRust(t *testing.T) {
	dangerous := [][]string{
		{"powershell", "-NoLogo", "-Command", "Start-Process 'https://example.com'"},
		{"powershell", "-Command", "Start-Process('https://example.com');"},
		{"powershell", "-Command", "Remove-Item test -Force"},
		{"powershell", "-Command", "Remove-Item -Path 'test' -Recurse -Force"},
		{"powershell", "-Command", "Remove-Item test -Force; Write-Host done"},
		{"powershell", "-Command", "if ($true) { Remove-Item test -Force}"},
		{"powershell", "-Command", "[void]( Remove-Item test -Force)]"},
		{"powershell", "-Command", "Write-Host hi;Remove-Item -Force C:\\tmp"},
		{"powershell", "-Command", "del,-Force,C:\\foo"},
		{"powershell", "-Command", "rm test -Force"},
		{"cmd", "/c", "echo", "hello", "&", "del", "/f", "file.txt"},
		{"cmd", "/c", "del /f file.txt"},
		{"cmd", "/c", "echo hello & del /f file.txt"},
		{"cmd", "/c", "echo hi&del /f file.txt"},
		{"cmd", "/c", "echo hi&&del /f file.txt"},
		{"cmd", "/c", "echo hi||del /f file.txt"},
		{"cmd", "/c", "echo hi&rmdir /s /q testdir"},
		{"cmd", "/c", "DEL", "/F", "file.txt"},
		{"cmd.exe", "/r", "del", "/f", "file.txt"},
		{"cmd", "/c", "start https://example.com"},
		{"cmd", "/c", `start "https://example.com"`},
		{"cmd", "/c", `start "" https://example.com`},
	}
	for _, command := range dangerous {
		if _, ok := MatchDangerousCommandForPlatform(command, DangerousCommandPlatformWindows); !ok {
			t.Fatalf("expected dangerous Windows command: %v", command)
		}
	}

	benign := [][]string{
		{"powershell", "-Command", "Get-ChildItem -Force; Remove-Item test"},
		{"cmd", "/c", "del", "C:/foo/bar.txt"},
		{"cmd", "/c", "rd", "C:/source"},
		{"cmd", "/c", "echo", "del", "/f"},
	}
	for _, command := range benign {
		if match, ok := MatchDangerousCommandForPlatform(command, DangerousCommandPlatformWindows); ok {
			t.Fatalf("benign Windows command matched %q: %v", match, command)
		}
	}
}

func TestDangerousCommandPlatformSkipsWindowsRulesOnPosixLikeRust(t *testing.T) {
	command := []string{"cmd", "/c", "del", "/f", "file.txt"}
	if match, ok := MatchDangerousCommandForPlatform(command, DangerousCommandPlatformPosix); ok {
		t.Fatalf("posix match = %q, want no match", match)
	}
}

func TestDangerousCommandPlatformForExecutorOSLikeRust(t *testing.T) {
	for _, test := range []struct {
		name string
		os   string
		want DangerousCommandPlatform
	}{
		{name: "linux", os: "linux", want: DangerousCommandPlatformPosix},
		{name: "macos", os: "macos", want: DangerousCommandPlatformPosix},
		{name: "windows", os: "windows", want: DangerousCommandPlatformWindows},
		{name: "unknown reported platform", os: "freebsd", want: DangerousCommandPlatformWindows},
		{name: "legacy executor", os: "", want: HostDangerousCommandPlatform()},
	} {
		if got := DangerousCommandPlatformForExecutorOS(test.os); got != test.want {
			t.Fatalf("%s: platform = %q, want %q", test.name, got, test.want)
		}
	}
}
