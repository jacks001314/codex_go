package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/history_cell"
	"codex_go/utils"
)

const fallbackMetadataWarningForTest = "Model metadata for `deepseek-v4-flash` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."

func TestRuntimeWarningEventCountsInFooterAndViewerLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 24})
	model.startupWarningsComplete = true

	updated, _ := model.Update(WarningEventMsg{Message: fallbackMetadataWarningForTest})
	model = updated.(*Model)
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "\u26a0 1 warning \u00b7 f2 to view") {
		t.Fatalf("footer notice missing runtime warning:\n%s", view)
	}
	if got := model.warningDisplay.WarningCount(); got != 1 {
		t.Fatalf("warning count = %d, want 1", got)
	}
	entries := historycell.WarningEntries(model.retainedWarningCells())
	if len(entries) != 1 || entries[0].Source != "Warning" {
		t.Fatalf("entries = %+v, want one Warning source", entries)
	}

	// Repeats for the same slug stay hidden, mirroring Rust's
	// WarningDisplayState::should_display.
	updated, _ = model.Update(WarningEventMsg{Message: fallbackMetadataWarningForTest})
	model = updated.(*Model)
	if got := len(model.warningCells); got != 1 {
		t.Fatalf("warning cells = %d, want 1", got)
	}
	if got := model.warningDisplay.WarningCount(); got != 1 {
		t.Fatalf("warning count after repeat = %d, want 1", got)
	}
}

// Before the startup window closes, Rust coalesces an ordinary warning into the
// StartupWarningsCell: every entry reports source "Startup" and the cell keeps
// its diagnostics sorted, so the config warning precedes the model warning.
func TestRuntimeWarningEventCoalescesIntoStartupWindowLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 24,
		StartupConfigWarnings: []string{
			"Codex is ignoring 1 unrecognized configuration setting. Check for typos or deprecated settings.",
		},
	})
	updated, _ := model.Update(WarningEventMsg{Message: fallbackMetadataWarningForTest})
	model = updated.(*Model)
	if got := len(model.warningCells); got != 0 {
		t.Fatalf("warning cells = %d, want 0 before startup completes", got)
	}
	entries := historycell.WarningEntries(model.retainedWarningCells())
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	for _, entry := range entries {
		if entry.Source != "Startup" {
			t.Fatalf("entry %+v has source %q, want Startup", entry, entry.Source)
		}
	}
	if !strings.Contains(entries[0].Details, "Codex is ignoring") {
		t.Fatalf("first entry = %+v, want the config warning first", entries[0])
	}
	if !strings.Contains(entries[1].Details, "Model metadata for") {
		t.Fatalf("second entry = %+v, want the model metadata warning", entries[1])
	}
	model.View()
	if got := model.warningDisplay.WarningCount(); got != 2 {
		t.Fatalf("warning count = %d, want 2", got)
	}
}

// A warning scoped to another thread must not touch the active thread's state.
func TestRuntimeWarningEventIgnoresForeignThread(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-1"
	model := NewModel(state, Options{Width: 120, Height: 24})
	model.startupWarningsComplete = true
	updated, _ := model.Update(WarningEventMsg{ThreadID: "thread-2", Message: "other thread warning"})
	model = updated.(*Model)
	if got := len(model.warningCells); got != 0 {
		t.Fatalf("warning cells = %d, want 0", got)
	}
}
