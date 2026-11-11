package windowssandbox

import "testing"

func TestArgvToCommandLinePreservesQuotedExecutableInCmdScript(t *testing.T) {
	argv := []string{
		"cmd.exe",
		"/c",
		`"C:\Program Files\PowerShell\7\pwsh.exe" -NoProfile -EncodedCommand abc==`,
	}
	got := ArgvToCommandLine(argv)
	want := `"cmd.exe" /s /c ""C:\Program Files\PowerShell\7\pwsh.exe" -NoProfile -EncodedCommand abc=="`
	if got != want {
		t.Fatalf("ArgvToCommandLine() = %q, want %q", got, want)
	}
}

func TestCmdScriptCommandLinePreservesQuotesInHashCommand(t *testing.T) {
	argv := []string{
		`C:\Windows\System32\CMD.EXE`,
		"/C",
		`certutil -hashfile "C:\New folder\long name.pftrace" SHA256`,
	}
	got, ok := CmdScriptCommandLine(argv)
	if !ok {
		t.Fatal("CmdScriptCommandLine() did not recognize cmd /c")
	}
	want := `"C:\Windows\System32\CMD.EXE" /s /c "certutil -hashfile "C:\New folder\long name.pftrace" SHA256"`
	if got != want {
		t.Fatalf("CmdScriptCommandLine() = %q, want %q", got, want)
	}
}

func TestCmdScriptCommandLineRejectsOtherPrograms(t *testing.T) {
	for _, argv := range [][]string{
		{"powershell.exe", "/c", "echo hi"},
		{"cmd.exe", "/d", "echo hi"},
		{"cmd.exe", "/c"},
	} {
		if got, ok := CmdScriptCommandLine(argv); ok {
			t.Fatalf("CmdScriptCommandLine(%#v) = %q, true; want false", argv, got)
		}
	}
}

func TestQuoteWindowsArgDoublesTrailingBackslashes(t *testing.T) {
	got := QuoteWindowsArg(`C:\Program Files\Codex\`)
	want := `"C:\Program Files\Codex\\"`
	if got != want {
		t.Fatalf("QuoteWindowsArg() = %q, want %q", got, want)
	}
}
