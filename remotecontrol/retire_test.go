package remotecontrol

import (
	"context"
	"testing"
)

// TestManagerRetireForAuthChangeClearsSession covers Rust #44341: a logout or
// account switch retires the live session and leaves remote control disabled
// until it is enabled again, discarding the previous owner's state.
func TestManagerRetireForAuthChangeClearsSession(t *testing.T) {
	manager := NewManager("codex", "install-1")
	if _, _ = manager.Enable(&EnableParams{Ephemeral: true}); manager.Status().Status != StatusConnected {
		t.Fatalf("status after enable = %q, want connected", manager.Status().Status)
	}
	manager.mu.Lock()
	manager.enrollment = &Enrollment{AccountID: "account-a", EnvironmentID: "env-1"}
	manager.clientByEnv = map[string]map[string]Client{"env-1": {"client-1": {ClientID: "client-1"}}}
	manager.pairings = map[string]*pairing{"code": {code: "code", envID: "env-1"}}
	manager.mu.Unlock()

	if err := manager.RetireForAuthChange(context.Background()); err != nil {
		t.Fatalf("RetireForAuthChange: %v", err)
	}
	if status := manager.Status(); status.Status != StatusDisabled {
		t.Fatalf("status after retirement = %q, want disabled", status.Status)
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.enrollment != nil {
		t.Fatalf("enrollment survived retirement: %#v", manager.enrollment)
	}
	if len(manager.clientByEnv) != 0 || len(manager.pairings) != 0 {
		t.Fatalf("session state survived retirement: clients=%#v pairings=%#v", manager.clientByEnv, manager.pairings)
	}
}

func TestManagerRetireForAuthChangeWithoutSessionIsNoOp(t *testing.T) {
	manager := NewManager("codex", "install-1")
	if err := manager.RetireForAuthChange(context.Background()); err != nil {
		t.Fatalf("RetireForAuthChange on disabled manager = %v", err)
	}
	if status := manager.Status(); status.Status != StatusDisabled {
		t.Fatalf("status = %q, want disabled", status.Status)
	}
}
