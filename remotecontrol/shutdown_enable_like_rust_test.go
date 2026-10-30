package remotecontrol

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
)

type shutdownTestDoer struct {
	mu    sync.Mutex
	calls int
}

func (d *shutdownTestDoer) Do(request *http.Request) (*http.Response, error) {
	d.mu.Lock()
	d.calls++
	d.mu.Unlock()
	<-request.Context().Done()
	return nil, request.Context().Err()
}

func (d *shutdownTestDoer) callCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls
}

func newShutdownTestManager(t *testing.T, target *RemoteControlTarget, store *EnrollmentStore, doer *shutdownTestDoer) *Manager {
	t.Helper()
	manager := NewManager("shutdown-test-server", "installation-id")
	manager.ConfigureBackend(&ManagerBackendOptions{
		Target: target,
		Store:  store,
		AuthLoader: func(context.Context) (*RemoteControlConnectionAuth, error) {
			return &RemoteControlConnectionAuth{AccountID: "account-a"}, nil
		},
		ServerAPIOptions: &ServerAPIOptions{HTTPClient: doer},
	})
	return manager
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestEnableAfterShutdownUsesInMemoryEnrollmentLikeRust(t *testing.T) {
	store := newTestEnrollmentStore(t)
	target := mustTarget(t, "https://chatgpt.com/remote/control")
	disabled := false
	enrollment := &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_cached",
		ServerID:            "srv_cached",
		ServerName:          "cached-server",
	}
	if err := UpdatePersistedRemoteControlEnrollment(context.Background(), store, target, "account-a", nil, enrollment, &disabled); err != nil {
		t.Fatalf("persist enrollment error = %v", err)
	}
	doer := &shutdownTestDoer{}
	manager := newShutdownTestManager(t, target, store, doer)
	if err := manager.replaceCurrentEnrollment(enrollment); err != nil {
		t.Fatalf("replaceCurrentEnrollment() error = %v", err)
	}

	response, _, err := manager.EnableContext(cancelledContext(), &EnableParams{})
	if err != nil {
		t.Fatalf("EnableContext() error = %v, want cached enrollment to enable after shutdown", err)
	}
	if response.EnvironmentID == nil || *response.EnvironmentID != "env_cached" {
		t.Fatalf("response environment = %+v, want env_cached", response.EnvironmentID)
	}
	if doer.callCount() != 0 {
		t.Fatalf("backend calls = %d, want none for a cached enrollment", doer.callCount())
	}
	if manager.Status().Status != StatusConnecting {
		t.Fatalf("status = %q, want connecting", manager.Status().Status)
	}
	record, err := store.GetRemoteControlEnrollment(context.Background(), target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("GetRemoteControlEnrollment() error = %v", err)
	}
	if record == nil || record.RemoteControlEnabled == nil || !*record.RemoteControlEnabled {
		t.Fatalf("persisted record = %+v, want enabled", record)
	}
}

func TestEnableAfterShutdownUsesPersistedEnrollmentLikeRust(t *testing.T) {
	store := newTestEnrollmentStore(t)
	target := mustTarget(t, "https://chatgpt.com/remote/control")
	disabled := false
	enrollment := &Enrollment{
		RemoteControlTarget: target,
		AccountID:           "account-a",
		EnvironmentID:       "env_persisted",
		ServerID:            "srv_persisted",
		ServerName:          "persisted-server",
	}
	if err := UpdatePersistedRemoteControlEnrollment(context.Background(), store, target, "account-a", nil, enrollment, &disabled); err != nil {
		t.Fatalf("persist enrollment error = %v", err)
	}
	doer := &shutdownTestDoer{}
	manager := newShutdownTestManager(t, target, store, doer)

	response, _, err := manager.EnableContext(cancelledContext(), &EnableParams{})
	if err != nil {
		t.Fatalf("EnableContext() error = %v, want persisted enrollment to enable after shutdown", err)
	}
	if response.EnvironmentID == nil || *response.EnvironmentID != "env_persisted" {
		t.Fatalf("response environment = %+v, want env_persisted", response.EnvironmentID)
	}
	if doer.callCount() != 0 {
		t.Fatalf("backend calls = %d, want none for a persisted enrollment", doer.callCount())
	}
	record, err := store.GetRemoteControlEnrollment(context.Background(), target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("GetRemoteControlEnrollment() error = %v", err)
	}
	if record == nil || record.RemoteControlEnabled == nil || !*record.RemoteControlEnabled {
		t.Fatalf("persisted record = %+v, want enabled", record)
	}
}

func TestEnableAfterShutdownCancelsMissingEnrollmentLikeRust(t *testing.T) {
	store := newTestEnrollmentStore(t)
	target := mustTarget(t, "https://chatgpt.com/remote/control")
	doer := &shutdownTestDoer{}
	manager := newShutdownTestManager(t, target, store, doer)

	_, _, err := manager.EnableContext(cancelledContext(), &EnableParams{})
	if !errors.Is(err, ErrRemoteControlShuttingDown) {
		t.Fatalf("EnableContext() error = %v, want ErrRemoteControlShuttingDown", err)
	}
	if doer.callCount() != 0 {
		t.Fatalf("backend calls = %d, want none after shutdown", doer.callCount())
	}
	if manager.Status().Status != StatusDisabled {
		t.Fatalf("status = %q, want disabled", manager.Status().Status)
	}
	record, err := store.GetRemoteControlEnrollment(context.Background(), target.WebSocketURL, "account-a", nil)
	if err != nil {
		t.Fatalf("GetRemoteControlEnrollment() error = %v", err)
	}
	if record != nil {
		t.Fatalf("persisted record = %+v, want none", record)
	}
}
