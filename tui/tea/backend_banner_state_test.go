package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func backendBannerStateModel(t *testing.T, raw *BackendBannerView, recovery BackendBannerRecoveryInput) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.Model = "gpt-5.2"
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnReadBackendBanner: func() (BackendBannerRead, error) {
			return BackendBannerRead{Banner: raw, Recovery: recovery}, nil
		},
	})
	runTeaCmd(t, model, model.Init())
	return model
}

// TestModelBackendBannerVisibilityFollowsSelectedModel pins Rust's
// blocked_model_slug/fallback_model_slugs visibility rule.
func TestModelBackendBannerVisibilityFollowsSelectedModel(t *testing.T) {
	blocked := "gpt-5"
	banner := &BackendBannerView{
		Title:              "Usage limit reached",
		BlockedModelSlug:   &blocked,
		FallbackModelSlugs: []string{"gpt-5.1", "gpt-5.2"},
	}
	model := backendBannerStateModel(t, banner, BackendBannerRecoveryInput{})
	if model.BackendBanner() == nil {
		t.Fatal("banner should be visible for a selected fallback model")
	}
	model.State.Model = "gpt-5.3"
	if model.BackendBanner() != nil {
		t.Fatal("banner should be hidden for a model outside the fallback list")
	}
	model.State.Model = "gpt-5"
	if model.BackendBanner() != nil {
		t.Fatal("banner should be hidden for the blocked model itself")
	}

	slug := "gpt-5.2"
	slugBanner := &BackendBannerView{Title: "Notice", ModelSlug: &slug}
	slugModel := backendBannerStateModel(t, slugBanner, BackendBannerRecoveryInput{})
	if slugModel.BackendBanner() == nil {
		t.Fatal("model_slug banner should be visible for the matching model")
	}
	slugModel.State.Model = "gpt-5"
	if slugModel.BackendBanner() != nil {
		t.Fatal("model_slug banner should be hidden for another model")
	}
}

// TestModelBackendBannerLunaReserveVisibleUntilDismissed pins the reserve special
// case and the recovery copy override.
func TestModelBackendBannerLunaReserveVisibleUntilDismissed(t *testing.T) {
	banner := &BackendBannerView{
		BannerType:  BackendBannerLunaReserve,
		Title:       "Reserve copy",
		Description: "Reserve description",
		Dismissible: true,
	}
	model := backendBannerStateModel(t, banner, BackendBannerRecoveryInput{})
	view := model.View()
	if !strings.Contains(view, "Usage limit reached") || strings.Contains(view, "Reserve copy") {
		t.Fatalf("reserve banner copy was not overridden off Reserve:\n%s", view)
	}
	model.Update(key(bubbletea.KeyEsc))
	if model.BackendBanner() != nil {
		t.Fatal("dismissed reserve banner should stay hidden")
	}
}

// TestModelBackendBannerOrdinaryUsageRecoveryLikeRust pins the identity-validated
// recovery gate.
func TestModelBackendBannerOrdinaryUsageRecoveryLikeRust(t *testing.T) {
	allowed := true
	recovered := BackendBannerRecoveryInput{AccountID: "acct", OrdinaryUsageAllowed: &allowed}
	model := backendBannerStateModel(t, nil, recovered)
	if !model.backendBanner.ordinaryUsageRecovered {
		t.Fatal("ordinary usage should be recovered when allowed with no blocker")
	}

	for name, input := range map[string]BackendBannerRecoveryInput{
		"missing decision": {AccountID: "acct"},
		"upsell present":   {AccountID: "acct", OrdinaryUsageAllowed: &allowed, HasRateLimitUpsell: true},
		"spend control":    {AccountID: "acct", OrdinaryUsageAllowed: &allowed, SpendControlReached: boolValue(true)},
		"reached type":     {AccountID: "acct", OrdinaryUsageAllowed: &allowed, RateLimitReachedType: "rate_limit_reached"},
	} {
		blocked := backendBannerStateModel(t, nil, input)
		if blocked.backendBanner.ordinaryUsageRecovered {
			t.Fatalf("%s: recovery authorized", name)
		}
	}

	// Denied ordinary usage is still recoverable with usable credits.
	denied := false
	withCredits := backendBannerStateModel(t, nil, BackendBannerRecoveryInput{
		AccountID: "acct", OrdinaryUsageAllowed: &denied,
		HasCreditsSnapshot: true, HasCredits: true,
	})
	if !withCredits.backendBanner.ordinaryUsageRecovered {
		t.Fatal("usable credits should authorize recovery")
	}
}

// TestModelBackendBannerSameOccurrenceKeepsDismissal pins Rust's same_occurrence
// comparison: an identical read keeps the dismissal, a changed occurrence resets
// it.
func TestModelBackendBannerSameOccurrenceKeepsDismissal(t *testing.T) {
	resetAt := int64(1_700_000_000)
	banner := &BackendBannerView{
		BannerType:  "usage_limit",
		Title:       "Usage limit reached",
		AccountID:   "acct",
		ResetAt:     &resetAt,
		Dismissible: true,
	}
	model := backendBannerStateModel(t, banner, BackendBannerRecoveryInput{AccountID: "acct"})
	model.Update(key(bubbletea.KeyEsc))
	if model.BackendBanner() != nil {
		t.Fatal("banner should be dismissed")
	}
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Banner:   banner,
		Recovery: BackendBannerRecoveryInput{AccountID: "acct"},
	}})
	if model.BackendBanner() != nil {
		t.Fatal("the same occurrence must stay dismissed")
	}
	changedReset := int64(1_800_000_000)
	changed := *banner
	changed.ResetAt = &changedReset
	model.applyBackendBannerResult(BackendBannerResultMsg{Read: BackendBannerRead{
		Banner:   &changed,
		Recovery: BackendBannerRecoveryInput{AccountID: "acct"},
	}})
	if model.BackendBanner() == nil {
		t.Fatal("a new occurrence should be visible again")
	}
}

func boolValue(value bool) *bool { return &value }
