package tea

import (
	"testing"

	appsapi "codex_go/apps"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

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
