package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	appsapi "codex_go/apps"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// TestAppListFailureShowsRetryablePopupLikeRust covers #43074: a failed app
// directory request shows a generic failure popup with a Retry action (no raw
// error), and retrying restores the loading state before the result.
func TestAppListFailureShowsRetryablePopupLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	calls := 0
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnReadApps: func(threadID string, forceRefetch bool) (appsapi.AppListResponse, error) {
			if !forceRefetch {
				t.Fatal("the apps request should force a refresh")
			}
			calls++
			if calls == 1 {
				return appsapi.AppListResponse{}, errors.New("network down: super-secret-token")
			}
			return appsapi.AppListResponse{Data: []appsapi.AppEntry{{ID: "drive", Name: "Drive", IsAccessible: true}}}, nil
		},
	})

	cmd := model.applyAppsCommand()
	if cmd == nil {
		t.Fatal("applyAppsCommand returned no fetch command")
	}
	if model.modal == nil || !strings.Contains(model.modal.body, "Loading installed and available apps") {
		t.Fatalf("loading view missing: %#v", model.modal)
	}

	updated, _ := model.Update(cmd())
	model = updated.(*Model)
	if model.modal == nil || model.modal.id != chatwidget.AppsSelectionViewID {
		t.Fatalf("failure did not open the apps popup: %#v", model.modal)
	}
	if !strings.Contains(model.modal.body, "Failed to load apps.") {
		t.Fatalf("failure popup body = %q", model.modal.body)
	}
	if strings.Contains(model.modal.body, "super-secret-token") || strings.Contains(model.View(), "super-secret-token") {
		t.Fatalf("raw request error leaked into the UI: %q", model.View())
	}

	// The Retry item is the second option; its number shortcut re-runs the
	// request and restores the loading state.
	updated, retry := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune{'2'}})
	model = updated.(*Model)
	if retry == nil {
		t.Fatal("retry returned no fetch command")
	}
	if model.modal == nil || !strings.Contains(model.modal.body, "Loading installed and available apps") {
		t.Fatalf("retry did not restore the loading state: %#v", model.modal)
	}

	updated, _ = model.Update(retry())
	model = updated.(*Model)
	if model.modal == nil || !strings.Contains(model.modal.body, "Use $ to insert an installed app") {
		t.Fatalf("retry result did not open the catalog: %#v", model.modal)
	}
	if calls != 2 {
		t.Fatalf("apps reader calls = %d, want 2", calls)
	}
}

func TestApplyAppListResultIgnoresStaleScopeLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.appsScopeGeneration = 3

	// A result tagged with the current scope is applied.
	model.applyAppListResult(AppListResultMsg{
		ThreadID:        "thread-1",
		ScopeGeneration: 3,
		Response: appsapi.AppListResponse{Data: []appsapi.AppEntry{
			{ID: "drive", Name: "Drive", IsAccessible: true},
		}},
	})
	if model.modal == nil || model.modal.id != chatwidget.AppsSelectionViewID {
		t.Fatalf("current-scope result did not open the apps view: modal=%#v", model.modal)
	}

	// A result from a stale scope is discarded and the open view dismissed.
	model.applyAppListResult(AppListResultMsg{
		ThreadID:        "old-thread",
		ScopeGeneration: 2,
		Response: appsapi.AppListResponse{Data: []appsapi.AppEntry{
			{ID: "stale", Name: "Stale", IsAccessible: true},
		}},
	})
	if model.modal != nil {
		t.Fatalf("stale result left the apps view open: modal=%#v", model.modal)
	}
	if len(model.modalOptionsForTest()) != 0 {
		t.Fatalf("stale result left apps options behind")
	}
}

func TestInvalidateAppsScopeDismissesOpenAppsView(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.openAppsView(appsapi.AppListResponse{Data: []appsapi.AppEntry{
		{ID: "drive", Name: "Drive", IsAccessible: true},
	}})
	if model.modal == nil || model.modal.id != chatwidget.AppsSelectionViewID {
		t.Fatalf("apps view did not open: modal=%#v", model.modal)
	}
	before := model.appsScopeGeneration
	model.invalidateAppsScope()
	if model.appsScopeGeneration != before+1 {
		t.Fatalf("scope generation = %d, want %d", model.appsScopeGeneration, before+1)
	}
	if model.modal != nil {
		t.Fatalf("apps view not dismissed by invalidateAppsScope: modal=%#v", model.modal)
	}
}

func (m *Model) modalOptionsForTest() []ModalOption {
	if m == nil || m.modal == nil {
		return nil
	}
	return append([]ModalOption(nil), m.modal.options...)
}
