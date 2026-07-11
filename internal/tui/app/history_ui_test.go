package app

import (
	"strings"
	"testing"

	historycell "codex_go/internal/tui/history_cell"
)

func TestDesktopThreadMessagesMatchRust(t *testing.T) {
	if DesktopThreadOpenedMessage != "Opened this session in Codex Desktop." {
		t.Fatalf("DesktopThreadOpenedMessage = %q", DesktopThreadOpenedMessage)
	}
	if got := DesktopThreadOpenErrorMessage("launch failed"); got != "Failed to open this session in Codex Desktop: launch failed. Install or launch Codex Desktop and try again." {
		t.Fatalf("DesktopThreadOpenErrorMessage() = %q", got)
	}
	if got := DesktopThreadOpenErrorMessage(" launch failed "); got != "Failed to open this session in Codex Desktop:  launch failed . Install or launch Codex Desktop and try again." {
		t.Fatalf("DesktopThreadOpenErrorMessage() preserved whitespace = %q", got)
	}
	if got := DesktopThreadURL(" thread-1 "); got != "codex://threads/ thread-1 " {
		t.Fatalf("DesktopThreadURL() = %q", got)
	}
}

func TestDesktopThreadHistoryCellsMatchRustSnapshots(t *testing.T) {
	opened := historycell.NewInfoEvent(DesktopThreadOpenedMessage, "")
	if got := strings.Join(opened.DisplayLines(80), "\n"); got != "• Opened this session in Codex Desktop." {
		t.Fatalf("opened history = %q", got)
	}

	err := historycell.NewErrorEvent(DesktopThreadOpenErrorMessage("launch failed"))
	if got := strings.Join(err.DisplayLines(80), "\n"); got != "■ Failed to open this session in Codex Desktop: launch failed. Install or launch Codex Desktop and try again." {
		t.Fatalf("error history = %q", got)
	}
}

func TestWindowsDesktopAppLaunchScriptMatchesRustShape(t *testing.T) {
	if got := PowershellSingleQuotedString("codex://threads/it's"); got != "'codex://threads/it''s'" {
		t.Fatalf("PowershellSingleQuotedString() = %q", got)
	}
	script := WindowsDesktopAppLaunchScript("codex://threads/it's")
	for _, want := range []string{
		"$ErrorActionPreference = 'Stop'",
		"$url = 'codex://threads/it''s'",
		"Get-AppxPackage -Name OpenAI.Codex",
		"Codex.exe",
		"resources\\app.asar",
		"Start-Process",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("WindowsDesktopAppLaunchScript missing %q:\n%s", want, script)
		}
	}
}

func TestHistoryUIStateInsertAndClear(t *testing.T) {
	var state HistoryUIState
	state.InsertRows([]string{"one", "two"})
	state.InsertRows(nil)
	if len(state.Rows) != 2 || state.Rows[0] != "one" || state.Rows[1] != "two" {
		t.Fatalf("Rows after insert = %#v", state.Rows)
	}
	state.Clear()
	if state.Rows != nil {
		t.Fatalf("Rows after clear = %#v, want nil", state.Rows)
	}
}
