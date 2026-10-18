package tea

import (
	"errors"
	"strings"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// TestWindowsSandboxPromptHidesDisallowedElevatedLikeRust covers Rust #45821:
// requirements that disallow the elevated setup remove that option.
func TestWindowsSandboxPromptHidesDisallowedElevatedLikeRust(t *testing.T) {
	view := chatwidget.NewWindowsSandboxEnablePromptView(true, true, false)
	for _, item := range view.Items {
		if item.Action == chatwidget.WindowsSandboxActionSetupElevated {
			t.Fatalf("elevated setup remained disallowed: %#v", view.Items)
		}
	}
	fallback := chatwidget.NewWindowsSandboxFallbackPromptView(true, false, false)
	for _, item := range fallback.Items {
		if item.Action == chatwidget.WindowsSandboxActionSetupElevated {
			t.Fatalf("fallback kept disallowed elevated setup: %#v", fallback.Items)
		}
	}
}

// TestWindowsSandboxSetupRefreshesStateFirstLikeRust covers the pre-setup
// app-server read and disallowed-mode rejection.
func TestWindowsSandboxSetupRefreshesStateFirstLikeRust(t *testing.T) {
	starts := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		OnReadWindowsSandboxState: func(string) (WindowsSandboxState, error) {
			return WindowsSandboxState{
				Level: chatwidget.WindowsSandboxLevelUnelevated,
				Requirements: chatwidget.PermissionRequirements{
					AllowedWindowsSandboxModes: []chatwidget.WindowsSandboxMode{chatwidget.WindowsSandboxModeUnelevated},
				},
			}, nil
		},
		OnStartWindowsSandboxSetup: func(chatwidget.WindowsSandboxMode, string) (WindowsSandboxSetupOutcome, error) {
			starts++
			return WindowsSandboxSetupOutcome{Started: true}, nil
		},
	})
	model.State.SetThreadID("thread-a")
	runTeaCmd(t, model, model.applyWindowsSandboxSetupCommand(chatwidget.WindowsSandboxModeElevated))
	if starts != 0 {
		t.Fatalf("disallowed elevated setup started %d times", starts)
	}
	if !strings.Contains(model.notice, windowsSandboxDisallowedNotice) {
		t.Fatalf("notice = %q", model.notice)
	}

	validated := model.applyWindowsSandboxSetupCommand(chatwidget.WindowsSandboxModeUnelevated)()
	updated, startCmd := model.Update(validated)
	model = updated.(*Model)
	runTeaCmd(t, model, startCmd)
	if starts != 1 {
		t.Fatalf("allowed setup starts = %d, want 1", starts)
	}
}

// TestWindowsSandboxStateRefreshOnCwdChangeLikeRust covers directory-scoped
// app-server requirements refreshing when a thread moves.
func TestWindowsSandboxStateRefreshOnCwdChangeLikeRust(t *testing.T) {
	reads := []string{}
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		OnReadWindowsSandboxState: func(cwd string) (WindowsSandboxState, error) {
			reads = append(reads, cwd)
			return WindowsSandboxState{
				Level: chatwidget.WindowsSandboxLevelUnelevated,
				Requirements: chatwidget.PermissionRequirements{
					AllowedWindowsSandboxModes: []chatwidget.WindowsSandboxMode{chatwidget.WindowsSandboxModeUnelevated},
				},
			}, nil
		},
	})
	model.State.SetThreadID("thread-a")
	model.State.CWD = "D:/old"
	updated, cmd := model.Update(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{CWD: "D:/new"},
	})
	model = updated.(*Model)
	runTeaCmd(t, model, cmd)
	if len(reads) != 1 || reads[0] != "D:/new" {
		t.Fatalf("sandbox reads = %#v", reads)
	}
	if model.windowsSandboxLevel != chatwidget.WindowsSandboxLevelUnelevated {
		t.Fatalf("level = %q", model.windowsSandboxLevel)
	}
	config := model.permissionMenuConfig(false)
	if !config.WindowsDegradedSandbox {
		t.Fatal("permission menu did not adopt the app-server sandbox level")
	}
}

// TestWindowsSandboxStateReadFailureRetainsStateLikeRust covers graceful
// recovery when the app-server read fails.
func TestWindowsSandboxStateReadFailureRetainsStateLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:  100,
		Height: 30,
		OnReadWindowsSandboxState: func(string) (WindowsSandboxState, error) {
			return WindowsSandboxState{}, errors.New("boom")
		},
	})
	model.windowsSandboxLevel = chatwidget.WindowsSandboxLevelElevated
	model.permissionRequirements = chatwidget.PermissionRequirements{
		AllowedWindowsSandboxModes: []chatwidget.WindowsSandboxMode{chatwidget.WindowsSandboxModeElevated},
	}
	model.applyWindowsSandboxStateMsg(WindowsSandboxStateMsg{Err: errors.New("boom")})
	if model.windowsSandboxLevel != chatwidget.WindowsSandboxLevelElevated ||
		len(model.permissionRequirements.AllowedWindowsSandboxModes) != 1 {
		t.Fatalf("state was replaced after a read failure: %#v", model.permissionRequirements)
	}
	if !strings.Contains(modelMessageText(model), "Could not read Windows sandbox configuration") {
		t.Fatalf("missing read failure message: %q", modelMessageText(model))
	}
}
