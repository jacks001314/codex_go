package tea

import (
	"context"
	"errors"
	"strings"
	"testing"

	historycell "codex_go/tui/history_cell"
)

func statuslineProviderDiscoveryEntries(model *Model) []historycell.WarningEntry {
	entries := []historycell.WarningEntry{}
	for _, entry := range model.warningDisplay.VisibleEntries(model.retainedWarningCells()) {
		if entry.ID.Kind == historycell.WarningIdStatuslineDiscovery {
			entries = append(entries, entry)
		}
	}
	return entries
}

// Rust #61993: discovery failures reach the warnings viewer with sanitized
// server names, bounded summaries, and no raw RPC details. Cancelling the picker
// keeps the warning; a successful scan clears it.
func TestStatuslineProviderDiscoveryWarningsLikeRust(t *testing.T) {
	discover := func(context.Context, StatuslineProviderDiscoveryRequest) <-chan StatuslineProviderDiscoveryUpdate {
		updates := make(chan StatuslineProviderDiscoveryUpdate, 1)
		batch := []StatuslineProviderDiscoveryUpdate{
			{Err: errors.New("inventory transport detail")},
			{Server: "fixture\tserver", Err: errors.New("raw provider detail"), TimedOut: true},
		}
		for _, name := range []string{"one", "two", "three", "four", "five"} {
			batch = append(batch, StatuslineProviderDiscoveryUpdate{Server: name, Err: errors.New("raw request detail")})
		}
		updates <- StatuslineProviderDiscoveryUpdate{Server: "", Providers: nil}
		close(updates)
		updates = make(chan StatuslineProviderDiscoveryUpdate, 1)
		updates <- StatuslineProviderDiscoveryUpdate{}
		close(updates)
		return updates
	}
	model := statuslineProviderPickerModel(t, nil, discover, &[]SettingsEdit{})
	model.openStatusLineSetup()
	model.toggleStatusSetupSelection()
	model.Update(statuslinePickerDiscoveryMsg{
		scan: model.statuslinePicker.scan,
		updates: []StatuslineProviderDiscoveryUpdate{
			{Err: errors.New("inventory transport detail")},
			{Server: "fixture\tserver", Err: errors.New("raw provider detail"), TimedOut: true},
			{Server: "one", Err: errors.New("raw request detail")},
			{Server: "two", Err: errors.New("raw request detail")},
			{Server: "three", Err: errors.New("raw request detail")},
			{Server: "four", Err: errors.New("raw request detail")},
			{Server: "five", Err: errors.New("raw request detail")},
		},
	})
	model.warningDisplay.SyncWarnings(model.retainedWarningCells())
	entries := statuslineProviderDiscoveryEntries(model)
	if len(entries) != 1 {
		t.Fatalf("discovery warnings = %#v", entries)
	}
	details := entries[0].Details
	if entries[0].Source != "Status line provider discovery" {
		t.Fatalf("discovery source = %q", entries[0].Source)
	}
	for _, want := range []string{
		"Global provider inventory: request failed.",
		"fixture server: no result by the 5-second discovery deadline.",
		"Additional affected servers: 2.",
	} {
		if !strings.Contains(details, want) {
			t.Fatalf("discovery details = %q, want %q", details, want)
		}
	}
	for _, raw := range []string{"inventory transport detail", "raw provider detail", "raw request detail"} {
		if strings.Contains(details, raw) {
			t.Fatalf("discovery details leaked %q: %q", raw, details)
		}
	}
	if got := model.warningDisplay.WarningCount(); got != 1 {
		t.Fatalf("warning count = %d", got)
	}

	// Cancelling the picker keeps the warning.
	model.respondModal(true)
	model.warningDisplay.SyncWarnings(model.retainedWarningCells())
	if entries := statuslineProviderDiscoveryEntries(model); len(entries) != 1 {
		t.Fatalf("cancelled scan dropped the warning: %#v", entries)
	}

	// A successful scan clears it.
	model.openStatusLineSetup()
	model.toggleStatusSetupSelection()
	model.Update(statuslinePickerDiscoveryMsg{
		scan: model.statuslinePicker.scan,
		updates: []StatuslineProviderDiscoveryUpdate{{
			Providers: []StatuslineProviderCandidate{{Server: "fixture", Tool: "line", Title: "Fixture Line"}},
		}},
	})
	model.warningDisplay.SyncWarnings(model.retainedWarningCells())
	if entries := statuslineProviderDiscoveryEntries(model); len(entries) != 0 {
		t.Fatalf("successful scan kept the warning: %#v", entries)
	}
}
