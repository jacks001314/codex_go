package tea

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/remotecontrol"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

func remoteControlTestModel(t *testing.T, run RemoteControlRunFunc) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{
		Width:              100,
		Height:             30,
		LocalDaemonSession: true,
		RemoteControlRun:   run,
	})
}

func applyRemoteControlSlash(t *testing.T, model *Model) {
	t.Helper()
	invocation, ok := codextui.ParseCommand("/rc")
	if !ok || invocation.Command != codextui.CommandRemoteControl {
		t.Fatalf("ParseCommand(/rc) = %#v ok=%v", invocation, ok)
	}
	runTeaCmd(t, model, model.applyCommand(invocation))
}

func remoteControlBool(value bool) *bool {
	return &value
}

// TestRemoteControlSlashOpensMenuLikeRust pins the `/rc` flow: the command reads
// the daemon status and shows the menu, whose rows follow that status.
func TestRemoteControlSlashOpensMenuLikeRust(t *testing.T) {
	var asked []chatwidget.RemoteControlAction
	model := remoteControlTestModel(t, func(_ context.Context, action chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error) {
		asked = append(asked, action)
		return chatwidget.RemoteControlResponse{Status: remotecontrol.StatusConnected, Saved: remoteControlBool(true)}, nil
	})
	applyRemoteControlSlash(t, model)
	if len(asked) != 1 || asked[0] != chatwidget.RemoteControlActionMenu {
		t.Fatalf("daemon actions = %#v, want one status read", asked)
	}
	if model.modal == nil || model.modal.id != chatwidget.RemoteControlMenuViewID {
		t.Fatalf("modal = %#v, want the remote control menu", model.modal)
	}
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Remote control") || !strings.Contains(view, "Disable") {
		t.Fatalf("/rc menu = %s", view)
	}
	if !strings.Contains(view, "Applies to local sessions; cloud sessions are unaffected.") {
		t.Fatalf("/rc menu missing the local-session note:\n%s", view)
	}
}

// TestRemoteControlWithoutDaemonShowsUnavailableLikeRust keeps the single
// disabled row Rust shows when no verified daemon backs the session.
func TestRemoteControlWithoutDaemonShowsUnavailableLikeRust(t *testing.T) {
	model := remoteControlTestModel(t, nil)
	applyRemoteControlSlash(t, model)
	if model.modal == nil || model.modal.id != chatwidget.RemoteControlMenuViewID {
		t.Fatalf("modal = %#v, want the remote control menu", model.modal)
	}
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, remoteControlUnavailable) {
		t.Fatalf("/rc menu missing the unavailable reason:\n%s", view)
	}
	if !strings.Contains(view, "Enable (disabled)") {
		t.Fatalf("/rc menu missing the disabled row:\n%s", view)
	}
}

// TestRemoteControlMenuRunsActionsLikeRust drives the menu rows: enabling asks
// the daemon and refreshes the status, and Add device shows the pairing code.
func TestRemoteControlMenuRunsActionsLikeRust(t *testing.T) {
	var asked []chatwidget.RemoteControlAction
	status := remotecontrol.StatusDisabled
	model := remoteControlTestModel(t, func(_ context.Context, action chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error) {
		asked = append(asked, action)
		switch action {
		case chatwidget.RemoteControlActionMenu:
			return chatwidget.RemoteControlResponse{Status: status, Saved: remoteControlBool(false)}, nil
		case chatwidget.RemoteControlActionEnable:
			status = remotecontrol.StatusConnected
			return chatwidget.RemoteControlResponse{Status: status, Saved: remoteControlBool(true)}, nil
		case chatwidget.RemoteControlActionAddDevice:
			code := "ABCD-EFGH"
			return chatwidget.RemoteControlResponse{ManualCode: &code}, nil
		}
		return chatwidget.RemoteControlResponse{}, nil
	})
	applyRemoteControlSlash(t, model)
	view := utils.StripANSI(model.viewString())
	if !strings.Contains(view, "Enable") || strings.Contains(view, "Disable") {
		t.Fatalf("/rc menu = %s, want the Enable row", view)
	}
	if !strings.Contains(view, "Enable remote control first") {
		t.Fatalf("/rc menu missing the Add device prerequisite:\n%s", view)
	}

	// Activating Enable asks the daemon and re-renders the refreshed status.
	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if len(asked) != 2 || asked[1] != chatwidget.RemoteControlActionEnable {
		t.Fatalf("daemon actions = %#v, want the enable request", asked)
	}
	if model.modal == nil || model.modal.id != chatwidget.RemoteControlMenuViewID {
		t.Fatalf("modal = %#v, want the refreshed menu", model.modal)
	}
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "Disable") {
		t.Fatalf("refreshed menu = %s, want the Disable row", view)
	}

	// Add device shows the manual pairing code.
	model.modal.selected = 1
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != chatwidget.RemoteControlPairingViewID {
		t.Fatalf("modal = %#v, want the pairing view", model.modal)
	}
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "ABCD-EFGH") {
		t.Fatalf("pairing view = %s", view)
	}
}

// TestRemoteControlRunnerFailureShowsUnavailableLikeRust reports the reason a
// status read could not reach the daemon instead of offering actions.
func TestRemoteControlRunnerFailureShowsUnavailableLikeRust(t *testing.T) {
	model := remoteControlTestModel(t, func(context.Context, chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error) {
		return chatwidget.RemoteControlResponse{}, errors.New("Codex could not verify that it manages the local background server.")
	})
	applyRemoteControlSlash(t, model)
	if model.modal == nil || model.modal.id != chatwidget.RemoteControlMenuViewID {
		t.Fatalf("modal = %#v, want the remote control menu", model.modal)
	}
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, "could not verify that it manages the local background server") {
		t.Fatalf("/rc menu missing the verification reason:\n%s", view)
	}
	if model.remoteControlPending {
		t.Fatal("a finished operation left the pending guard set")
	}
}

// TestRemoteControlDisableReportsInfoLikeRust shows the disable acknowledgement
// instead of another menu.
func TestRemoteControlDisableReportsInfoLikeRust(t *testing.T) {
	model := remoteControlTestModel(t, func(_ context.Context, action chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error) {
		return chatwidget.RemoteControlResponse{Status: remotecontrol.StatusDisabled, Saved: remoteControlBool(false)}, nil
	})
	model.remoteControlRun = func(_ context.Context, action chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error) {
		return chatwidget.RemoteControlResponse{Status: remotecontrol.StatusDisabled, Saved: remoteControlBool(false)}, nil
	}
	runTeaCmd(t, model, model.startRemoteControl(chatwidget.RemoteControlActionDisable))
	if !strings.Contains(model.notice, "Remote control is disabled for local sessions. Cloud sessions are unaffected.") {
		t.Fatalf("notice = %q", model.notice)
	}
}

// TestRemoteControlPendingGuardLikeRust refuses a second operation while one is
// still running, as Rust's remote_control_pending flag does.
func TestRemoteControlPendingGuardLikeRust(t *testing.T) {
	model := remoteControlTestModel(t, func(context.Context, chatwidget.RemoteControlAction) (chatwidget.RemoteControlResponse, error) {
		return chatwidget.RemoteControlResponse{}, nil
	})
	model.remoteControlPending = true
	invocation, ok := codextui.ParseCommand("/rc")
	if !ok {
		t.Fatal("ParseCommand(/rc) failed")
	}
	if cmd := model.applyCommand(invocation); cmd != nil {
		t.Fatal("a pending operation still started another status read")
	}
	if view := utils.StripANSI(model.viewString()); !strings.Contains(view, remoteControlPendingMessage) {
		t.Fatalf("/rc pending guard = %s", view)
	}
}
