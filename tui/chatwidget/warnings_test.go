package chatwidget

import (
	"testing"

	"codex_go/tui/history_cell"
)

func TestWarningDisplayStateDeduplicatesFallbackModelMetadataWarnings(t *testing.T) {
	var state WarningDisplayState
	warning := "Model metadata for `gpt-test` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
	if !state.ShouldDisplay(warning) {
		t.Fatal("first warning should display")
	}
	if state.ShouldDisplay(warning) {
		t.Fatal("duplicate fallback model metadata warning should not display")
	}
	other := "Model metadata for `gpt-other` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."
	if !state.ShouldDisplay(other) {
		t.Fatal("different fallback model metadata warning should display")
	}
	if !state.ShouldDisplay("plain warning") || !state.ShouldDisplay("plain warning") {
		t.Fatal("plain warnings should not be deduplicated")
	}
}

func TestFallbackModelMetadataWarningSlug(t *testing.T) {
	got, ok := FallbackModelMetadataWarningSlug("Model metadata for `gpt-test` not found. Defaulting to fallback metadata; this can degrade performance and cause issues.")
	if !ok || got != "gpt-test" {
		t.Fatalf("slug = %q ok=%v", got, ok)
	}
	if got, ok := FallbackModelMetadataWarningSlug(" Model metadata for `gpt-test` not found. Defaulting to fallback metadata; this can degrade performance and cause issues. "); ok || got != "" {
		t.Fatalf("spaced warning should not match Rust exact prefix/suffix: slug = %q ok=%v", got, ok)
	}
	emptySlug := "Model metadata for `" + fallbackModelMetadataWarningSuffix
	if got, ok := FallbackModelMetadataWarningSlug(emptySlug); !ok || got != "" {
		t.Fatalf("empty slug should still match like Rust: slug = %q ok=%v", got, ok)
	}
	if got, ok := FallbackModelMetadataWarningSlug("Model metadata missing"); ok || got != "" {
		t.Fatalf("non fallback slug = %q ok=%v", got, ok)
	}
}

// Mirrors `WarningDisplayState::visible_entries` and the `UpdateWarnings` arm:
// a dismissal hides only the exact details it recorded, an identity that gained
// details is visible again, keeping a matching entry clears the dismissal, and
// the footer count follows the visible diagnostics.
func TestWarningDisplayDismissalsLikeRust(t *testing.T) {
	first := historycell.NewMCPStartupWarnings([]string{"alpha failed"}, []string{"alpha"}, "")
	cells := []historycell.WarningCell{first}

	var state WarningDisplayState
	state.SyncWarnings(cells)
	if state.WarningCount() != 1 {
		t.Fatalf("count before dismissal = %d, want 1", state.WarningCount())
	}
	entries := state.VisibleEntries(cells)
	if len(entries) != 1 {
		t.Fatalf("visible entries = %#v", entries)
	}
	state.ApplyDecisions(entries, nil)
	if got := state.VisibleEntries(cells); len(got) != 0 {
		t.Fatalf("dismissed entry still visible: %#v", got)
	}
	state.SyncWarnings(cells)
	if state.WarningCount() != 0 {
		t.Fatalf("count after dismissal = %d, want 0", state.WarningCount())
	}

	// The same identity with more details is shown again.
	grown := []historycell.WarningCell{historycell.NewMCPStartupWarnings([]string{"alpha failed", "alpha retried"}, []string{"alpha"}, "")}
	if got := state.VisibleEntries(grown); len(got) != 1 || got[0].Details != "alpha failed\n\nalpha retried" {
		t.Fatalf("grown identity entries = %#v", got)
	}

	// Keeping a matching entry clears its dismissal.
	state.ApplyDecisions(nil, entries)
	if got := state.VisibleEntries(cells); len(got) != 1 {
		t.Fatalf("kept entry still dismissed: %#v", got)
	}
}
