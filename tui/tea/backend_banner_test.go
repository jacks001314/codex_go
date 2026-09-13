package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func backendBannerTestModel(t *testing.T, banner *BackendBannerView, readErr error) (*Model, *[]BackendBannerAction) {
	t.Helper()
	dispatched := &[]BackendBannerAction{}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnReadBackendBanner: func() (BackendBannerRead, error) {
			return BackendBannerRead{Banner: banner}, readErr
		},
		OnBackendBannerAction: func(action BackendBannerAction) bubbletea.Cmd {
			*dispatched = append(*dispatched, action)
			return nil
		},
	})
	return model, dispatched
}

// TestModelBackendBannerRendersCopyAndCTAs pins Rust #44857's inline
// actionable banner: validated title/description plus the numbered CTAs.
func TestModelBackendBannerRendersCopyAndCTAs(t *testing.T) {
	model, _ := backendBannerTestModel(t, &BackendBannerView{
		Title:       "Usage limit reached",
		Description: "Buy credits to keep working.",
		Actions: []BackendBannerAction{
			{Label: "Buy credits", Kind: BannerActionOpenURL, URL: "https://example.com/credits"},
			{Label: "View usage", Kind: BannerActionOpenURL, URL: "https://example.com/usage"},
		},
		Dismissible: true,
	}, nil)
	runTeaCmd(t, model, model.Init())
	if model.BackendBanner() == nil {
		t.Fatal("banner was not installed by the startup read")
	}
	view := model.View()
	for _, want := range []string{
		"Usage limit reached",
		"Buy credits to keep working.",
		"Buy credits",
		"View usage",
		"esc to dismiss",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("banner view missing %q:\n%s", want, view)
		}
	}
}

// TestModelBackendBannerDispatchesSelectedCTA pins the numbered CTA dispatch.
func TestModelBackendBannerDispatchesSelectedCTA(t *testing.T) {
	model, dispatched := backendBannerTestModel(t, &BackendBannerView{
		Title: "Usage limit reached",
		Actions: []BackendBannerAction{
			{Label: "Buy credits", Kind: BannerActionOpenURL, URL: "https://example.com/credits"},
			{Label: "View usage", Kind: BannerActionOpenURL, URL: "https://example.com/usage"},
		},
	}, nil)
	runTeaCmd(t, model, model.Init())

	if _, cmd := model.Update(runes("2")); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if len(*dispatched) != 1 || (*dispatched)[0].Label != "View usage" {
		t.Fatalf("dispatched = %v, want the second CTA", *dispatched)
	}
	// An out-of-range digit is not a banner action.
	if _, cmd := model.Update(runes("9")); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if len(*dispatched) != 1 {
		t.Fatalf("out-of-range digit dispatched: %v", *dispatched)
	}
}

// TestModelBackendBannerEscDismissesOnlyWhenDismissible pins the dismissal
// rule: a persistent banner ignores Esc.
func TestModelBackendBannerEscDismissesOnlyWhenDismissible(t *testing.T) {
	model, _ := backendBannerTestModel(t, &BackendBannerView{
		Title:       "Persistent notice",
		Dismissible: false,
	}, nil)
	runTeaCmd(t, model, model.Init())
	model.Update(key(bubbletea.KeyEsc))
	if !strings.Contains(model.View(), "Persistent notice") {
		t.Fatal("persistent banner was dismissed by esc")
	}

	dismissible, _ := backendBannerTestModel(t, &BackendBannerView{
		Title:       "Dismissible notice",
		Dismissible: true,
	}, nil)
	runTeaCmd(t, dismissible, dismissible.Init())
	dismissible.Update(key(bubbletea.KeyEsc))
	if strings.Contains(dismissible.View(), "Dismissible notice") {
		t.Fatalf("dismissible banner did not close on esc:\n%s", dismissible.View())
	}
}

// TestModelBackendBannerYieldsToDraftAndRunningTask pins Rust's key gate: the
// banner does not consume keys while a draft exists or a task runs.
func TestModelBackendBannerYieldsToDraftAndRunningTask(t *testing.T) {
	model, dispatched := backendBannerTestModel(t, &BackendBannerView{
		Title:       "Usage limit reached",
		Actions:     []BackendBannerAction{{Label: "Buy credits", Kind: BannerActionOpenURL}},
		Dismissible: true,
	}, nil)
	runTeaCmd(t, model, model.Init())
	model.composer.InsertString("draft")
	if _, cmd := model.Update(runes("1")); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if len(*dispatched) != 0 {
		t.Fatalf("banner consumed a key while a draft existed: %v", *dispatched)
	}

	model.composer.SetValue("")
	model.State.Status = "running"
	if _, cmd := model.Update(runes("1")); cmd != nil {
		runTeaCmd(t, model, cmd)
	}
	if len(*dispatched) != 0 {
		t.Fatalf("banner consumed a key while a task ran: %v", *dispatched)
	}
}

// TestModelBackendBannerResetUsageStaysInModel pins that the reset-credits CTA
// opens the model's own reset view instead of reaching the app callback.
func TestModelBackendBannerResetUsageStaysInModel(t *testing.T) {
	model, dispatched := backendBannerTestModel(t, &BackendBannerView{
		Title:   "Reset available",
		Actions: []BackendBannerAction{{Label: "Reset usage", Kind: BannerActionResetUsage}},
	}, nil)
	runTeaCmd(t, model, model.Init())
	_, cmd := model.Update(runes("1"))
	runTeaCmd(t, model, cmd)
	if len(*dispatched) != 0 {
		t.Fatalf("reset-usage CTA reached the app callback: %v", *dispatched)
	}
}

// TestModelBackendBannerDismissesOnNewTurn pins Rust's
// dismiss_backend_banner_for_new_turn: a shown, dismissible banner hides when
// the user starts a new turn, while a persistent banner stays.
func TestModelBackendBannerDismissesOnNewTurn(t *testing.T) {
	model, _ := backendBannerTestModel(t, &BackendBannerView{
		Title:       "Usage limit reached",
		Dismissible: true,
	}, nil)
	runTeaCmd(t, model, model.Init())
	_ = model.View() // the banner must render once before a new turn dismisses it
	model.composer.InsertString("keep going")
	if _, cmd := model.Update(key(bubbletea.KeyEnter)); cmd != nil {
		t.Fatalf("submitting a prompt returned an unexpected command: %T", cmd)
	}
	if strings.Contains(model.View(), "Usage limit reached") {
		t.Fatalf("dismissible banner survived a new turn:\n%s", model.View())
	}

	persistent, _ := backendBannerTestModel(t, &BackendBannerView{Title: "Persistent notice"}, nil)
	runTeaCmd(t, persistent, persistent.Init())
	_ = persistent.View()
	persistent.composer.InsertString("keep going")
	persistent.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(persistent.View(), "Persistent notice") {
		t.Fatal("persistent banner was dismissed by a new turn")
	}
}

// TestModelBackendBannerFailedReadKeepsPreviousBanner pins the transient-read
// behavior.
func TestModelBackendBannerFailedReadKeepsPreviousBanner(t *testing.T) {
	model, _ := backendBannerTestModel(t, &BackendBannerView{Title: "First banner"}, nil)
	runTeaCmd(t, model, model.Init())
	model.applyBackendBannerResult(BackendBannerResultMsg{Err: errors.New("read failed")})
	if banner := model.BackendBanner(); banner == nil || banner.Title != "First banner" {
		t.Fatalf("banner = %#v, want the previous banner", banner)
	}
	model.applyBackendBannerResult(BackendBannerResultMsg{})
	if model.BackendBanner() != nil {
		t.Fatal("a successful empty read should clear the banner")
	}
}
