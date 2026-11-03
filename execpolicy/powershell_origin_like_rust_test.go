package execpolicy

import "testing"

// TestPowerShellCommandsAreIndependentLikeRust pins Rust's
// powershell_commands_are_independent: only scripts without pipelines, alias or
// item mutation cmdlets, and state-sharing launcher arguments may classify each
// lowered command on its own.
func TestPowerShellCommandsAreIndependentLikeRust(t *testing.T) {
	cases := []struct {
		name     string
		script   string
		commands [][]string
		want     bool
	}{
		{name: "literal chain", script: "pwd && ls", commands: [][]string{{"pwd"}, {"ls"}}, want: true},
		{name: "pipeline", script: "Get-Content a | Measure-Object", commands: [][]string{{"Get-Content", "a"}, {"Measure-Object"}}, want: false},
		{name: "set-alias", script: "Set-Alias foo cmd.exe", commands: [][]string{{"Set-Alias", "foo", "cmd.exe"}}, want: false},
		{name: "new-item", script: "New-Item foo", commands: [][]string{{"New-Item", "foo"}}, want: false},
		{name: "launcher argument", script: "echo Start-Process https://x", commands: [][]string{{"echo", "Start-Process", "https://x"}}, want: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := PowerShellCommandsAreIndependent(test.script, test.commands); got != test.want {
				t.Fatalf("PowerShellCommandsAreIndependent(%q) = %v, want %v", test.script, got, test.want)
			}
		})
	}
}

// TestDangerousPowerShellLiteralWordsLeadOnlyLikeRust pins the difference
// between the opaque and literal classifiers: a lowered literal command only
// treats its leading word as a command entry point.
func TestDangerousPowerShellLiteralWordsLeadOnlyLikeRust(t *testing.T) {
	words := []string{"echo", "start", "https://example.com"}
	if _, ok := MatchDangerousPowerShellWordsForPlatform(words, DangerousCommandPlatformWindows); !ok {
		t.Fatal("opaque PowerShell rules should flag a `start` token anywhere in the invocation")
	}
	if _, ok := MatchDangerousPowerShellLiteralWords(words); ok {
		t.Fatal("literal rules must not treat a later argument as a command entry point")
	}
	if _, ok := MatchDangerousPowerShellLiteralWords([]string{"Remove-Item", "test", "-Force"}); !ok {
		t.Fatal("literal rules should flag a force delete cmdlet")
	}
}

// TestDangerousPowerShellWrapperPrefersLiteralLoweringLikeRust pins Rust's
// is_dangerous_powershell: when the `-Command` body strictly lowers to
// independent literal commands, those commands decide the verdict; scripts the
// literal parser cannot see keep the flat token scan.
func TestDangerousPowerShellWrapperPrefersLiteralLoweringLikeRust(t *testing.T) {
	benign := [][]string{
		{"powershell", "-Command", "echo start https://example.com"},
		{"powershell", "-Command:echo start https://example.com"},
		{"pwsh", "-NoProfile", "-Command", "echo hi"},
	}
	for _, command := range benign {
		if match, ok := MatchDangerousCommandForPlatform(command, DangerousCommandPlatformWindows); ok {
			t.Fatalf("literal wrapper %v matched %q, want no match", command, match)
		}
	}

	dangerous := [][]string{
		{"powershell", "-Command", "Write-Host hi; start https://example.com"},
		{"powershell", "-Command", "Remove-Item test -Force"},
		// The literal parser rejects `$`, so the flat token scan runs and sees
		// the standalone launcher verb.
		{"powershell", "-Command", "echo $x; start https://example.com"},
	}
	for _, command := range dangerous {
		if match, ok := MatchDangerousCommandForPlatform(command, DangerousCommandPlatformWindows); !ok {
			t.Fatalf("wrapper %v should be dangerous, got %q", command, match)
		}
	}
}
