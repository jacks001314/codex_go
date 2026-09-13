package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func backendBannerFallbackModel(t *testing.T, currentModel string) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.Model = currentModel
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.State.HasChatGPTAccount = true
	return model
}

// TestModelBackendBannerFallbackSwitchesToFallback pins Rust's non-reserve
// fallback: a blocked model with an ordered fallback list switches to the first
// picker-visible candidate and reports it.
func TestModelBackendBannerFallbackSwitchesToFallback(t *testing.T) {
	model := backendBannerFallbackModel(t, "gpt-5")
	blocked := "gpt-5"
	blockedModel := &blocked
	model.modelCatalogOpts = []codextui.ModelPickerOption{
		{ID: "gpt-5", ShowInPicker: true},
		{ID: "gpt-5.1", Label: "GPT-5.1", ShowInPicker: false},
		{ID: "gpt-5.2", Label: "GPT-5.2", ShowInPicker: true},
	}
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{Banner: &BackendBannerView{
		BlockedModelSlug:   blockedModel,
		FallbackModelSlugs: []string{"gpt-5.1", "gpt-5.2"},
	}}})
	model.applyBackendBannerFallback()
	if model.State.Model != "gpt-5.2" {
		t.Fatalf("model = %q, want the first picker-visible fallback", model.State.Model)
	}
	if !strings.Contains(modelMessageText(model), "Automatically switched to GPT-5.2 due to usage limits.") {
		t.Fatalf("switch notice missing:\n%s", modelMessageText(model))
	}
}

// TestModelBackendBannerFallbackEntersReserve pins the Reserve entry: the banner
// authorizes the picker-hidden Reserve model and saves the return target.
func TestModelBackendBannerFallbackEntersReserve(t *testing.T) {
	model := backendBannerFallbackModel(t, "gpt-5")
	model.modelCatalogOpts = []codextui.ModelPickerOption{
		{ID: "gpt-5", ShowInPicker: true},
		{ID: LunaReserveModel, Label: "Luna Reserve", ShowInPicker: false},
	}
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Banner:   &BackendBannerView{BannerType: BackendBannerLunaReserve},
		Recovery: BackendBannerRecoveryInput{AccountID: "acct"},
	}})
	model.applyBackendBannerFallback()
	if model.State.Model != LunaReserveModel {
		t.Fatalf("model = %q, want the Reserve model", model.State.Model)
	}
	if model.reserveReturn == nil || model.reserveReturn.Model != "gpt-5" || model.reserveReturn.AccountID != "acct" {
		t.Fatalf("reserve return = %#v", model.reserveReturn)
	}
}

// TestModelBackendBannerFallbackRecoversFromReserve pins the ordinary-usage
// recovery: a validated read returns to the saved model and clears the target.
func TestModelBackendBannerFallbackRecoversFromReserve(t *testing.T) {
	model := backendBannerFallbackModel(t, LunaReserveModel)
	model.modelCatalogOpts = []codextui.ModelPickerOption{{ID: "gpt-5", Label: "GPT-5", ShowInPicker: true}}
	model.reserveReturn = &ReserveReturn{AccountID: "acct", Model: "gpt-5", Effort: "high"}
	allowed := true
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Recovery: BackendBannerRecoveryInput{AccountID: "acct", OrdinaryUsageAllowed: &allowed},
	}})
	model.applyBackendBannerFallback()
	if model.State.Model != "gpt-5" {
		t.Fatalf("model = %q, want the saved return model", model.State.Model)
	}
	if model.State.ReasoningEffort != "high" {
		t.Fatalf("effort = %q, want the saved return effort", model.State.ReasoningEffort)
	}
	if model.reserveReturn != nil {
		t.Fatalf("reserve return = %#v, want cleared", model.reserveReturn)
	}
	if !strings.Contains(modelMessageText(model), "Automatically switched back to GPT-5") {
		t.Fatalf("recovery notice missing:\n%s", modelMessageText(model))
	}
}

// TestModelBackendBannerFallbackRequiresCatalogVisibility pins that a saved
// return model the picker hides is not restored.
func TestModelBackendBannerFallbackRequiresCatalogVisibility(t *testing.T) {
	model := backendBannerFallbackModel(t, LunaReserveModel)
	model.modelCatalogOpts = []codextui.ModelPickerOption{
		{ID: "gpt-5", ShowInPicker: true},
		{ID: "gpt-hidden", ShowInPicker: false},
	}
	model.reserveReturn = &ReserveReturn{AccountID: "acct", Model: "gpt-hidden"}
	allowed := true
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Recovery: BackendBannerRecoveryInput{AccountID: "acct", OrdinaryUsageAllowed: &allowed},
	}})
	model.applyBackendBannerFallback()
	if model.State.Model != LunaReserveModel {
		t.Fatalf("model = %q, want to stay on Reserve", model.State.Model)
	}
}

// TestReserveReturnPersistenceRoundTrip pins the per-task cache format and the
// corrupt/missing behavior.
func TestReserveReturnPersistenceRoundTrip(t *testing.T) {
	home := t.TempDir()
	if got := loadReserveReturn(home, "thread-1"); got != nil {
		t.Fatalf("missing cache loaded %#v", got)
	}
	if err := saveReserveReturn(home, "thread-1", &ReserveReturn{AccountID: "acct", Model: "gpt-5", Effort: "high"}); err != nil {
		t.Fatal(err)
	}
	got := loadReserveReturn(home, "thread-1")
	if got == nil || got.AccountID != "acct" || got.Model != "gpt-5" || got.Effort != "high" {
		t.Fatalf("loaded cache = %#v", got)
	}
	clearReserveReturn(home, "thread-1")
	if got := loadReserveReturn(home, "thread-1"); got != nil {
		t.Fatalf("cleared cache loaded %#v", got)
	}
	// A corrupt cache must not be treated as a target.
	if err := os.MkdirAll(filepath.Dir(reserveReturnPath(home, "thread-2")), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reserveReturnPath(home, "thread-2"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadReserveReturn(home, "thread-2"); got != nil {
		t.Fatalf("corrupt cache loaded %#v", got)
	}
}

// TestModelBackendBannerFallbackPersistsReserveReturn pins the reconnect/resume
// behavior: entering Reserve saves the return target, a fresh model restores it
// for the recovery, and the recovery clears it.
func TestModelBackendBannerFallbackPersistsReserveReturn(t *testing.T) {
	home := t.TempDir()
	model := backendBannerFallbackModel(t, "gpt-5")
	model.codexHome = home
	model.modelCatalogOpts = []codextui.ModelPickerOption{
		{ID: "gpt-5", ShowInPicker: true},
		{ID: LunaReserveModel, ShowInPicker: false},
	}
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Banner:   &BackendBannerView{BannerType: BackendBannerLunaReserve},
		Recovery: BackendBannerRecoveryInput{AccountID: "acct"},
	}})
	model.applyBackendBannerFallback()
	if model.State.Model != LunaReserveModel {
		t.Fatalf("model = %q, want Reserve", model.State.Model)
	}
	if got := loadReserveReturn(home, "thread-1"); got == nil || got.Model != "gpt-5" || got.AccountID != "acct" {
		t.Fatalf("persisted return = %#v", got)
	}

	// A fresh model (reconnect) restores the saved target and clears it after
	// the recovery switch.
	recovered := backendBannerFallbackModel(t, LunaReserveModel)
	recovered.codexHome = home
	recovered.modelCatalogOpts = []codextui.ModelPickerOption{{ID: "gpt-5", Label: "GPT-5", ShowInPicker: true}}
	allowed := true
	recovered.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Recovery: BackendBannerRecoveryInput{AccountID: "acct", OrdinaryUsageAllowed: &allowed},
	}})
	recovered.applyBackendBannerFallback()
	if recovered.State.Model != "gpt-5" {
		t.Fatalf("recovered model = %q, want the persisted target", recovered.State.Model)
	}
	if got := loadReserveReturn(home, "thread-1"); got != nil {
		t.Fatalf("return target survived recovery: %#v", got)
	}
}

// TestModelBackendBannerContinueReserveAction pins Rust's local "Continue with
// Luna Reserve" choice: while the current model is Reserve, the surface offers
// it and selecting it dismisses the notice without a backend CTA.
func TestModelBackendBannerContinueReserveAction(t *testing.T) {
	model := backendBannerStateModel(t, &BackendBannerView{
		BannerType:  BackendBannerLunaReserve,
		Title:       "Reserve",
		Dismissible: true,
	}, BackendBannerRecoveryInput{AccountID: "acct"})
	model.State.Model = LunaReserveModel
	if !strings.Contains(model.View(), "Continue with Luna Reserve") {
		t.Fatalf("continue choice missing:\n%s", model.View())
	}
	model.Update(runes("1"))
	if model.BackendBanner() != nil {
		t.Fatal("continue choice did not dismiss the reserve notice")
	}
}

// TestModelBackendBannerFallbackRewritesQueuedSubmissions pins Rust's
// apply_reserve_fallback_to_pending_turn: a queued turn naming the replaced
// model is retargeted at the accepted one.
func TestModelBackendBannerFallbackRewritesQueuedSubmissions(t *testing.T) {
	model := backendBannerFallbackModel(t, "gpt-5")
	model.modelCatalogOpts = []codextui.ModelPickerOption{
		{ID: "gpt-5", ShowInPicker: true},
		{ID: LunaReserveModel, ShowInPicker: false},
	}
	oldEffort := "medium"
	model.State.Status = "idle"
	model.queued = []queuedSubmission{{Request: SubmitRequest{
		Prompt: "queued turn",
		Model:  "gpt-5",
		CollaborationMode: &chatwidget.CollaborationMode{
			Settings: chatwidget.CollaborationModeSettings{Model: "gpt-5", ReasoningEffort: &oldEffort},
		},
	}}}
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Banner:   &BackendBannerView{BannerType: BackendBannerLunaReserve},
		Recovery: BackendBannerRecoveryInput{AccountID: "acct"},
	}})
	runTeaCmd(t, model, model.applyBackendBannerFallback())
	if model.State.Model != LunaReserveModel {
		t.Fatalf("model = %q, want Reserve", model.State.Model)
	}
	if len(model.submitRequests) == 0 {
		t.Fatal("the released turn was not submitted")
	}
	released := model.submitRequests[len(model.submitRequests)-1]
	if released.Model != LunaReserveModel {
		t.Fatalf("released request model = %q, want the accepted model", released.Model)
	}
	if mode := released.CollaborationMode; mode == nil || mode.Settings.Model != LunaReserveModel {
		t.Fatalf("released collaboration mode = %#v", mode)
	}
}

// TestModelBackendBannerFallbackRequiresOpenAIAuth pins Rust's
// requires_openai_auth gate: a custom provider never receives an account-model
// switch.
func TestModelBackendBannerFallbackRequiresOpenAIAuth(t *testing.T) {
	model := backendBannerFallbackModel(t, "gpt-5")
	model.State.Provider = "custom-provider"
	blocked := "gpt-5"
	model.modelCatalogOpts = []codextui.ModelPickerOption{
		{ID: "gpt-5", ShowInPicker: true},
		{ID: "gpt-5.2", ShowInPicker: true},
	}
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{Banner: &BackendBannerView{
		BlockedModelSlug:   &blocked,
		FallbackModelSlugs: []string{"gpt-5.2"},
	}}})
	model.applyBackendBannerFallback()
	if model.State.Model != "gpt-5" {
		t.Fatalf("model = %q, want no switch for a non-OpenAI provider", model.State.Model)
	}
	if cmd := model.applyBackendBannerFallbackCmd(); cmd != nil {
		t.Fatalf("catalog fetch scheduled for a non-OpenAI provider: %T", cmd)
	}

	model.State.Provider = "openai"
	model.applyBackendBannerFallback()
	if model.State.Model != "gpt-5.2" {
		t.Fatalf("model = %q, want the fallback for the OpenAI provider", model.State.Model)
	}
}
