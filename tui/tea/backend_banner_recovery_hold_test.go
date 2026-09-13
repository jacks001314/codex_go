package tea

import (
	"strings"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// TestModelBackendBannerHoldsTurnsUntilReserveSwitch pins Rust's
// rate_limit_recovery hold: while the Reserve banner waits for the switch, a new
// prompt is queued instead of sent, and it is released after the switch.
func TestModelBackendBannerHoldsTurnsUntilReserveSwitch(t *testing.T) {
	model := backendBannerFallbackModel(t, "gpt-5")
	model.modelCatalogOpts = []codextui.ModelPickerOption{
		{ID: "gpt-5", ShowInPicker: true},
		{ID: LunaReserveModel, ShowInPicker: false},
	}
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Banner:   &BackendBannerView{BannerType: BackendBannerLunaReserve},
		Recovery: BackendBannerRecoveryInput{AccountID: "acct"},
	}})
	if !model.rateLimitRecoveryPending {
		t.Fatal("recovery hold was not set while waiting for Reserve")
	}
	model.composer.InsertString("keep going")
	model.submitComposer()
	if len(model.queued) != 1 {
		t.Fatalf("queued submissions = %d, want the held turn", len(model.queued))
	}
	if !strings.Contains(model.notice, "Waiting for the usage-limit model switch") {
		t.Fatalf("notice = %q", model.notice)
	}

	cmd := model.applyBackendBannerFallback()
	if model.State.Model != LunaReserveModel {
		t.Fatalf("model = %q, want Reserve", model.State.Model)
	}
	if model.rateLimitRecoveryPending {
		t.Fatal("recovery hold was not released after the switch")
	}
	runTeaCmd(t, model, cmd)
	if len(model.queued) != 0 {
		t.Fatalf("held turn was not released: %d queued", len(model.queued))
	}
}

// TestModelBackendBannerRecoveryHoldClearedByThreadSettings pins Rust's
// settings clearing the hold when the server reports a model change.
func TestModelBackendBannerRecoveryHoldClearedByThreadSettings(t *testing.T) {
	model := backendBannerFallbackModel(t, "gpt-5")
	model.rateLimitRecoveryPending = true
	model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-1",
		Settings: appserver.Settings{Model: "gpt-5.2"},
	})
	if model.rateLimitRecoveryPending {
		t.Fatal("thread settings model change left the recovery hold set")
	}
}
