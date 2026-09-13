package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func backendBannerFallbackModel(t *testing.T, currentModel string) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.Model = currentModel
	state.HasChatGPTAccount = true
	model := NewModel(state, Options{Width: 80, Height: 24})
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
