package remotecontrol

import (
	"context"
	"testing"
	"time"
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

// TestManagerRetireForAuthChangePersistsDespiteCallerCancellation covers Rust
// #44341's write-permit rule: a preference write admitted by retirement keeps
// its permit through caller cancellation, so logging out (which cancels the
// requesting context) still records the retired owner's disabled preference.
func TestManagerRetireForAuthChangePersistsDespiteCallerCancellation(t *testing.T) {
	store := newTestEnrollmentStore(t)
	target := mustTarget(t, "https://chatgpt.com/remote/control")
	manager := NewManagerWithBackend("codex", "install-1", &ManagerBackendOptions{
		Target:     target,
		Store:      store,
		AuthLoader: staticRemoteControlAuth("account-a"),
	})
	enabled := true
	enrollment := &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env-1",
		ServerID:            "srv-1",
		ServerName:          "server",
	}
	if err := UpdatePersistedRemoteControlEnrollment(context.Background(), store, target, "account-a", nil, enrollment, &enabled); err != nil {
		t.Fatalf("persist enabled enrollment: %v", err)
	}
	manager.mu.Lock()
	manager.status = StatusConnected
	manager.enrollment = enrollment
	manager.mu.Unlock()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.RetireForAuthChange(cancelled); err != nil {
		t.Fatalf("RetireForAuthChange: %v", err)
	}
	record, err := store.GetRemoteControlEnrollment(context.Background(), target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("load retirement record: %v", err)
	}
	if record.RemoteControlEnabled == nil || *record.RemoteControlEnabled {
		t.Fatalf("retired owner's disabled preference was not persisted: %#v", record)
	}
}

// TestEnrollmentStoreCloseDrainsAdmittedWrites covers the shutdown half of Rust
// #44341: Close waits for an admitted write instead of closing the database
// underneath it.
func TestEnrollmentStoreCloseDrainsAdmittedWrites(t *testing.T) {
	store := newTestEnrollmentStore(t)
	release := store.beginWrite() // simulate an admitted write in flight
	closed := make(chan error, 1)
	go func() {
		closed <- store.Close()
	}()
	select {
	case <-closed:
		t.Fatal("Close returned while a write was still admitted")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not finish after the admitted write drained")
	}
}
