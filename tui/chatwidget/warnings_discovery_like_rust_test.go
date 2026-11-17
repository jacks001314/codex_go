package chatwidget

import "testing"

// Rust #61993: incomplete statusline provider discovery is a virtual warning: it
// counts in the footer, can be dismissed like any other entry, is replaced by a
// newer scan, and disappears after a successful scan.
func TestWarningDisplayStatuslineDiscoveryLikeRust(t *testing.T) {
	state := WarningDisplayState{}
	state.SetStatuslineDiscoveryWarning(7, "fixture: request failed.")
	state.SyncWarnings(nil)
	if got := state.WarningCount(); got != 1 {
		t.Fatalf("warning count = %d", got)
	}
	entries := state.VisibleEntries(nil)
	if len(entries) != 1 {
		t.Fatalf("visible entries = %#v", entries)
	}
	if entries[0].Source != "Status line provider discovery" || entries[0].Details != "fixture: request failed." {
		t.Fatalf("discovery entry = %#v", entries[0])
	}

	state.ApplyDecisions(entries, nil)
	state.SyncWarnings(nil)
	if got := state.WarningCount(); got != 0 {
		t.Fatalf("dismissed discovery still counted: %d", got)
	}
	if entries := state.VisibleEntries(nil); len(entries) != 0 {
		t.Fatalf("dismissed discovery still visible: %#v", entries)
	}

	// A different scan replaces the dismissed revision and shows again.
	state.SetStatuslineDiscoveryWarning(8, "fixture: request failed.")
	state.SyncWarnings(nil)
	if got := state.WarningCount(); got != 1 {
		t.Fatalf("new scan count = %d", got)
	}
	if entries := state.VisibleEntries(nil); len(entries) != 1 {
		t.Fatalf("new scan entries = %#v", entries)
	}

	// A successful scan clears the warning and its dismissal bookkeeping.
	state.SetStatuslineDiscoveryWarning(9, "")
	state.SyncWarnings(nil)
	if got := state.WarningCount(); got != 0 {
		t.Fatalf("successful scan count = %d", got)
	}
	if entries := state.VisibleEntries(nil); len(entries) != 0 {
		t.Fatalf("successful scan entries = %#v", entries)
	}
}
