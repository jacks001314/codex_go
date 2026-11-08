package appserver

import (
	"context"
	"os"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/remotecontrol"
	"codex_go/session"
)

func remoteControlAuthRouter(t *testing.T, configBody string) *RuntimeRouter {
	t.Helper()
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(t.TempDir()), home, &RuntimeRouterOptions{})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func stubRemoteControlAuth(t *testing.T) remotecontrol.RemoteControlAuthLoader {
	t.Helper()
	return func(context.Context) (*remotecontrol.RemoteControlConnectionAuth, error) {
		return &remotecontrol.RemoteControlConnectionAuth{AccountID: "acct-1", AuthHeaders: &model.AuthHeaders{}}, nil
	}
}

// TestRemoteControlBackendBindsRuntimeAuthSlotLikeRust pins Rust #60073's
// app-server seam: when runtime auth is on, the remote-control loader produces
// connections bound to a filled runtime slot so each request re-reads it. With
// the feature off the connection keeps the actor credential.
func TestRemoteControlBackendBindsRuntimeAuthSlotLikeRust(t *testing.T) {
	enabled := remoteControlAuthRouter(t, "model_provider = \"openai\"\nfeatures = { runtime_agent_instances = true }\n")
	backend := enabled.remoteControlManagerBackend(enabled.services.Config.CodexHome(), &RuntimeRouterOptions{
		RemoteControlBackendEnabled: true,
		RemoteControlAuthLoader:     stubRemoteControlAuth(t),
	})
	if backend == nil || backend.AuthLoader == nil {
		t.Fatal("remote-control backend produced no auth loader")
	}
	bound, err := backend.AuthLoader(context.Background())
	if err != nil {
		t.Fatalf("auth loader error = %v", err)
	}
	if bound == nil || bound.RuntimeAuth == nil {
		t.Fatal("the remote-control connection was not bound to the runtime slot")
	}
	if bound.RuntimeAuth.Slot.Get() == nil {
		t.Fatal("the runtime slot bound to the connection is empty")
	}

	disabled := remoteControlAuthRouter(t, "model_provider = \"openai\"\n")
	plainBackend := disabled.remoteControlManagerBackend(disabled.services.Config.CodexHome(), &RuntimeRouterOptions{
		RemoteControlBackendEnabled: true,
		RemoteControlAuthLoader:     stubRemoteControlAuth(t),
	})
	plain, err := plainBackend.AuthLoader(context.Background())
	if err != nil {
		t.Fatalf("auth loader with the feature off error = %v", err)
	}
	if plain != nil && plain.RuntimeAuth != nil {
		t.Fatal("the feature being off still bound the connection to a runtime slot")
	}
}
