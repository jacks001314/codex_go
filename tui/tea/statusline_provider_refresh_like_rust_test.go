package tea

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/config"
)

// Rust #61985 statusline_explicit_reselection_restarts_a_failed_reader: saving
// the same provider again retries a read that had failed.
func TestStatuslineProviderReselectionRestartsFailedReaderLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	reads := 0
	model := statuslineProviderPollingModel(t,
		func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			reads++
			if reads == 1 {
				return nil, errors.New("provider disconnected")
			}
			return statuslineProviderContents(`"restored"`), nil
		})
	model.refreshStatusControls()
	runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if got := model.statusLineContent(); got != statuslineProviderFailureCopy() {
		t.Fatalf("failure copy = %q", got)
	}
	failed := model.statuslineProviderRead

	selection := config.StatusLineProviderSelection{Server: "fixture", Tool: "line"}
	model.Update(SettingsWriteResultMsg{
		Kind:   settingsWriteKindStatusLineProvider,
		Result: SettingsWriteResult{StatusLineProvider: &selection},
	})
	if model.statuslineProviderRead == nil || model.statuslineProviderRead == failed {
		t.Fatal("reselecting the provider did not restart its reader")
	}
	if got := model.statusLineContent(); got != statuslineProviderInitializing {
		t.Fatalf("reselection kept the failure copy: %q", got)
	}
	// Update already handed the staged read to the returned batch; the test runs
	// that read itself instead of drawing the batch tick.
	runProviderCmd(model, runProviderCmd(model, model.statuslineProviderResolveCmd(model.statuslineProviderRead)))
	if got := model.statusLineContent(); got != "restored" {
		t.Fatalf("retried provider footer = %q", got)
	}
}

// Rust #61985 reloading_the_effective_feature_gate: a config reload that turns
// the extensions gate off hides the provider and turning it back on restages it.
func TestStatuslineProviderConfigReloadTogglesGateLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	model := statuslineProviderPollingModel(t,
		func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return statuslineProviderContents(`"ready"`), nil
		})
	model.refreshStatusControls()
	if model.statuslineProviderRead == nil {
		t.Fatal("enabled gate did not stage the provider")
	}

	model.applyLocalSettingsValues(SettingsWriteResult{FeatureSettings: map[string]bool{"tui_extensions_statusline": false}})
	model.refreshStatusControls()
	if model.statuslineProviderRead != nil {
		t.Fatal("disabled gate kept the provider read")
	}
	if strings.Contains(model.statusLineContent(), statuslineProviderInitializing) {
		t.Fatalf("disabled gate kept provider content: %q", model.statusLineContent())
	}

	model.applyLocalSettingsValues(SettingsWriteResult{FeatureSettings: map[string]bool{"tui_extensions_statusline": true}})
	model.refreshStatusControls()
	if model.statuslineProviderRead == nil {
		t.Fatal("re-enabled gate did not restage the provider")
	}
	if got := model.statusLineContent(); got != statuslineProviderInitializing {
		t.Fatalf("re-enabled gate footer = %q", got)
	}
}

// Rust #61985 statusline_changing_provider_or_reconnecting_discards_the_previous_read:
// a disconnect cancels the read, hides provider content, and a reattached
// session restarts the provider from scratch.
func TestStatuslineProviderDisconnectShowsBuiltinAndReattachRestartsLikeRust(t *testing.T) {
	compressStatuslineProviderClock(t, 5*time.Millisecond, time.Second)
	model := statuslineProviderPollingModel(t,
		func(context.Context, StatuslineProviderResourceRequest) ([]StatuslineProviderContent, error) {
			return statuslineProviderContents(`"ready"`), nil
		})
	model.refreshStatusControls()
	runProviderCmd(model, runProviderCmd(model, model.ensureProviderFooterCmd()))
	if got := model.statusLineContent(); got != "ready" {
		t.Fatalf("provider footer = %q", got)
	}
	retired := model.statuslineProviderRead
	if retired == nil {
		t.Fatal("provider read was not staged")
	}

	model.Update(AppServerDisconnectMsg{})
	if model.statuslineProviderRead != nil {
		t.Fatal("disconnect kept the provider read")
	}
	if got := model.statusLineContent(); strings.Contains(got, "ready") {
		t.Fatalf("disconnect kept provider content: %q", got)
	}
	select {
	case <-retired.ctx.Done():
	default:
		t.Fatal("disconnect did not cancel the outstanding read")
	}
	model.applyStatuslineProviderRead(statuslineProviderReadMsg{
		token:    retired.token,
		threadID: retired.threadID,
		server:   retired.server,
		contents: statuslineProviderContents(`"stale"`),
	})
	if got := model.statusLineContent(); strings.Contains(got, "stale") {
		t.Fatalf("offline footer accepted a stale reply: %q", got)
	}

	// Rust's reconnect builds the next widget; here an attached thread is that
	// fresh session, so the provider read starts over.
	model.resetThreadScopedState()
	model.refreshStatusControls()
	if model.statuslineProviderRead == nil || model.statuslineProviderRead.token == retired.token {
		t.Fatal("reattach did not restart the provider read")
	}
	if got := model.statusLineContent(); got != statuslineProviderInitializing {
		t.Fatalf("reattach footer = %q", got)
	}
}
