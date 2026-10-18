package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

// TestDaemonSlashCommandOpensMenuAndConfirmsLikeRust pins the `/daemon` flow:
// the command opens the source menu, the source opens the confirmation, and
// confirming records the pending update and exits.
func TestDaemonSlashCommandOpensMenuAndConfirmsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:               100,
		Height:              30,
		LocalDaemonSession:  true,
		DaemonVersion:       "1.2.3",
		DaemonCLIExecutable: "/usr/local/bin/codex",
		DaemonCLIVersion:    "9.9.9",
		DaemonCLIPackage:    true,
	})
	invocation, ok := codextui.ParseCommand("/daemon")
	if !ok || invocation.Command != codextui.CommandDaemon {
		t.Fatalf("ParseCommand(/daemon) = %#v ok=%v", invocation, ok)
	}
	runTeaCmd(t, model, model.applyCommand(invocation))
	if model.modal == nil || model.modal.id != chatwidget.DaemonMenuViewID {
		t.Fatalf("modal = %#v, want the daemon menu", model.modal)
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Running daemon: 1.2.3") {
		t.Fatalf("/daemon menu missing the daemon status:\n%s", view)
	}

	// Selecting "Install latest public stable" opens the confirmation.
	model.modal.selected = 0
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != chatwidget.DaemonConfirmViewID {
		t.Fatalf("modal = %#v, want the daemon confirmation", model.modal)
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Update daemon and exit Codex?") {
		t.Fatalf("confirmation missing its title:\n%s", view)
	}

	// "Update and exit" records the pending action and exits.
	model.modal.selected = 1
	runTeaCmd(t, model, model.respondModal(false))
	if got := model.PendingUpdateAction(); got != codextui.UpdateActionDaemonPublicStable {
		t.Fatalf("pending update action = %q, want the public stable daemon action", got)
	}
}

// TestDaemonMenuDisablesUnavailableSourcesLikeRust pins that a remote session
// disables both sources, so no activation can arm an update.
func TestDaemonMenuDisablesUnavailableSourcesLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:               100,
		Height:              30,
		RemoteAppServer:     true,
		DaemonCLIExecutable: "/usr/local/bin/codex",
		DaemonCLIPackage:    true,
	})
	invocation, _ := codextui.ParseCommand("/daemon")
	runTeaCmd(t, model, model.applyCommand(invocation))
	if model.modal == nil || model.modal.id != chatwidget.DaemonMenuViewID {
		t.Fatalf("modal = %#v, want the daemon menu", model.modal)
	}
	for index := range model.modal.options {
		if !model.modal.options[index].Disabled && model.modal.options[index].DisabledReason == "" {
			t.Fatalf("option %d is enabled for a remote session", index)
		}
	}
	runTeaCmd(t, model, model.activateModalOption(0))
	if model.modal != nil && model.modal.id == chatwidget.DaemonConfirmViewID {
		t.Fatal("a disabled remote source opened the confirmation")
	}
	if got := model.PendingUpdateAction(); got != "" {
		t.Fatalf("pending update action = %q, want none", got)
	}
}

// Rust #53173: a verified managed daemon offers restart, confirms it, then
// exits so the host can restart and reconnect.
func TestManagedDaemonRestartConfirmationLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:               100,
		Height:              30,
		LocalDaemonSession:  true,
		DaemonManaged:       true,
		DaemonCLIExecutable: "/usr/local/bin/codex",
		DaemonCLIPackage:    true,
	})
	invocation, _ := codextui.ParseCommand("/daemon")
	runTeaCmd(t, model, model.applyCommand(invocation))
	model.modal.selected = 2
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != chatwidget.DaemonRestartConfirmViewID {
		t.Fatalf("modal = %#v, want restart confirmation", model.modal)
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Codex will reconnect automatically") {
		t.Fatalf("restart confirmation missing reconnect copy:\n%s", view)
	}
	model.modal.selected = 1
	if _, cmd := model.Update(key(bubbletea.KeyEnter)); cmd == nil {
		t.Fatal("restart confirmation did not exit")
	}
	if !model.PendingDaemonRestart() {
		t.Fatal("restart confirmation did not mark the pending restart")
	}
}

func TestDaemonRestartDisabledWhenUnverifiedLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:               100,
		Height:              30,
		LocalDaemonSession:  true,
		DaemonCLIExecutable: "/usr/local/bin/codex",
		DaemonCLIPackage:    true,
	})
	invocation, _ := codextui.ParseCommand("/daemon")
	runTeaCmd(t, model, model.applyCommand(invocation))
	if len(model.modal.options) < 3 || !model.modal.options[2].Disabled ||
		!strings.Contains(model.modal.options[2].DisabledReason, "hasn't verified") {
		t.Fatalf("restart option = %#v", model.modal.options)
	}
}
