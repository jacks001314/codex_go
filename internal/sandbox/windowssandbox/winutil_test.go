package windowssandbox

import "testing"

func TestArgvToCommandLineQuotesEachArgumentIndependently(t *testing.T) {
	argv := []string{
		"cmd.exe",
		"/c",
		`"C:\Program Files\PowerShell\7\pwsh.exe" -NoProfile -EncodedCommand abc==`,
	}
	got := ArgvToCommandLine(argv)
	want := `cmd.exe /c "\"C:\Program Files\PowerShell\7\pwsh.exe\" -NoProfile -EncodedCommand abc=="`
	if got != want {
		t.Fatalf("ArgvToCommandLine() = %q, want %q", got, want)
	}
}

func TestQuoteWindowsArgDoublesTrailingBackslashes(t *testing.T) {
	got := QuoteWindowsArg(`C:\Program Files\Codex\`)
	want := `"C:\Program Files\Codex\\"`
	if got != want {
		t.Fatalf("QuoteWindowsArg() = %q, want %q", got, want)
	}
}
