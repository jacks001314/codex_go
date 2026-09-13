package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// TestModelRateLimitSwitchPromptDefersToBackendBanner pins Rust's
// maybe_show_pending_rate_limit_prompt: while an applicable backend banner owns
// the account-recovery surface, a high-usage snapshot keeps the lower-cost
// prompt pending instead of opening it.
func TestModelRateLimitSwitchPromptDefersToBackendBanner(t *testing.T) {
	model := backendBannerStateModel(t, &BackendBannerView{Title: "Usage limit reached"}, BackendBannerRecoveryInput{})
	model.maybeOpenRateLimitSwitchPrompt(chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{UsedPercent: 95},
	})
	if model.modal != nil && model.modal.kind == ModalKindRateLimitSwitch {
		t.Fatal("switch prompt opened while a banner applies")
	}
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptPending {
		t.Fatalf("prompt state = %q, want pending", model.rateLimitSwitchPrompt)
	}
}

// TestModelBackendBannerDismissesShownSwitchPrompt pins Rust's presentation
// behavior: a shown switch prompt closes when a banner becomes applicable and is
// re-armed as pending.
func TestModelBackendBannerDismissesShownSwitchPrompt(t *testing.T) {
	model := backendBannerFallbackModel(t, "gpt-5")
	model.openRateLimitSwitchPrompt(codextui.ModelPickerOption{ID: chatwidget.NudgeModelSlug})
	model.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptShown
	if model.modal == nil || model.modal.kind != ModalKindRateLimitSwitch {
		t.Fatalf("switch prompt not open: %#v", model.modal)
	}
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Banner: &BackendBannerView{Title: "Usage limit reached"},
	}})
	if model.modal != nil && model.modal.kind == ModalKindRateLimitSwitch {
		t.Fatal("banner did not dismiss the shown switch prompt")
	}
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptPending {
		t.Fatalf("prompt state = %q, want pending", model.rateLimitSwitchPrompt)
	}
}

// TestModelRateLimitSwitchPromptIdleOnReserveModel pins Rust's reset to idle when
// the session runs on the Reserve model.
func TestModelRateLimitSwitchPromptIdleOnReserveModel(t *testing.T) {
	model := backendBannerFallbackModel(t, LunaReserveModel)
	model.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptPending
	model.maybeOpenRateLimitSwitchPrompt(chatwidget.RateLimitSnapshot{
		LimitID: "codex",
		Primary: &chatwidget.RateLimitWindow{UsedPercent: 95},
	})
	if model.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptIdle {
		t.Fatalf("prompt state = %q, want idle on Reserve", model.rateLimitSwitchPrompt)
	}
}
