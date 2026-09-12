package appserver

import (
	"context"
	"testing"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/remotecontrol"
)

func TestRetireRemoteControlForAuthChangeDisablesSession(t *testing.T) {
	manager := remotecontrol.NewManager("codex", "install-1")
	manager.Enable(&remotecontrol.EnableParams{Ephemeral: true})
	if manager.Status().Status != remotecontrol.StatusConnected {
		t.Fatalf("status after enable = %q", manager.Status().Status)
	}
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		Remote:       manager,
		ThreadExtras: NewThreadExtraService(),
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)

	router.retireRemoteControlForAuthChange(context.Background())
	if manager.Status().Status != remotecontrol.StatusDisabled {
		t.Fatalf("status after retirement = %q, want disabled", manager.Status().Status)
	}
	if !sinkHasMethod(sink, NotificationRemoteControlStatusChanged) {
		t.Fatalf("retirement did not publish a status change: %+v", sink.List())
	}
}

// TestRuntimeRouterLogoutRetiresRemoteControl covers Rust #44341: logging out
// retires the remote-control session and leaves it disabled.
func TestRuntimeRouterLogoutRetiresRemoteControl(t *testing.T) {
	home := t.TempDir()
	manager := remotecontrol.NewManager("codex", "install-1")
	manager.Enable(&remotecontrol.EnableParams{Ephemeral: true})
	router := NewRuntimeRouter(RuntimeServices{
		Account: auth.NewAccountManager(),
		Config:  config.NewConfigService(home),
		Remote:  manager,
	})
	router.SetNotificationSink(NewNotificationBuffer())

	login := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{Type: auth.AccountAPIKey, APIKey: "sk-test"}))
	if login.Error != nil {
		t.Fatalf("login = %+v", login)
	}
	logout := router.Handle(requestWithParams(t, IntID(2), MethodLogoutAccount, map[string]any{}))
	if logout.Error != nil {
		t.Fatalf("logout = %+v", logout)
	}
	if manager.Status().Status != remotecontrol.StatusDisabled {
		t.Fatalf("status after logout = %q, want disabled", manager.Status().Status)
	}
}
