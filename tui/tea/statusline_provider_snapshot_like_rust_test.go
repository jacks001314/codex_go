package tea

import (
	"context"
	"testing"
	"time"

	chatwidget "codex_go/tui/chatwidget"
)

// Rust #61986 provider_needs_snapshot: invalid provider content cancels the
// reader's snapshot consumers (thread usage and workspace headline) while the
// error footer stays up, and a reselection restores them.
func TestStatuslineProviderFailedReadStopsSnapshotConsumersLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	reads := 0
	model := statuslineProviderPollingModel(t,
		func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			reads++
			if reads == 1 {
				return statuslineProviderContents("{"), nil
			}
			return statuslineProviderContents(`"ready"`), nil
		})
	model.ensureStatusControls()
	model.statusControls.StatusLineConfigured = true
	model.statusControls.StatusLineIDs = []string{"model"}
	model.statusControls.TerminalTitleConfigured = true
	model.statusControls.TerminalTitleIDs = []string{}
	model.refreshStatusControls()
	runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if got := model.statusLineContent(); got != statuslineProviderFailureCopy() {
		t.Fatalf("failure copy = %q", got)
	}
	// Rust evaluates the snapshot gate on the next refresh_status_line; the
	// failure itself only cancels the reader.
	model.refreshStatusControls()

	if !model.statuslineProviderSelected() {
		t.Fatal("failed read stopped owning the footer")
	}
	if !model.statusControls.StatusLineProviderActive {
		t.Fatal("failed read cleared the provider footer flag")
	}
	if model.providerNeedsSnapshot() || model.statusControls.StatusLineProviderNeedsSnapshot {
		t.Fatal("failed read kept consuming the statusline snapshot")
	}
	if credits, cost := model.threadUsageSelection(); credits || cost {
		t.Fatalf("failed read kept provider usage = credits %v cost %v", credits, cost)
	}
	model.ensureThreadUsageForSurfaces(chatwidget.NewStatusSurfaceSelections([]string{"model"}, nil))
	if model.threadUsageSurfaceSelected {
		t.Fatal("failed read kept polling thread usage")
	}

	// Reselecting the provider restores both snapshot consumers.
	selection := model.StatusLineProvider()
	if selection == nil {
		t.Fatal("model lost its saved provider selection")
	}
	model.Update(SettingsWriteResultMsg{
		Kind:   settingsWriteKindStatusLineProvider,
		Result: SettingsWriteResult{StatusLineProvider: selection},
	})
	if !model.providerNeedsSnapshot() || !model.statusControls.StatusLineProviderNeedsSnapshot {
		t.Fatal("reselection did not restore the snapshot consumers")
	}
	if credits, cost := model.threadUsageSelection(); !credits || !cost {
		t.Fatalf("reselection usage = credits %v cost %v", credits, cost)
	}
}
