package execserver

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"
)

// TestFSWatchMissingIsLinuxOnlyLikeRust pins Rust #57598's
// `cfg!(target_os = "linux")` gate: other executors acknowledge the request but
// refuse to watch.
func TestFSWatchMissingIsLinuxOnlyLikeRust(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux executors advertise fs/watchMissing")
	}
	server := &Server{}
	response := server.watchMissing(&FSWatchMissingParams{WatchID: 1, Missing: []string{"file:///nonexistent"}}, nil)
	if response == nil || response.Watching {
		t.Fatalf("response = %#v, want watching=false", response)
	}
	// Newer ids still supersede; older or equal ids are ignored.
	if response := server.watchMissing(&FSWatchMissingParams{WatchID: 1, Missing: []string{"file:///nonexistent"}}, nil); response.Watching {
		t.Fatalf("equal watch id unexpectedly watched")
	}
}

// TestEnvironmentCapabilitiesFSWatchMissingShapeLikeRust pins the optional wire
// field: legacy executors omit it and decode as unsupported.
func TestEnvironmentCapabilitiesFSWatchMissingShapeLikeRust(t *testing.T) {
	legacy := EnvironmentCapabilities{}
	if legacy.FSWatchMissing {
		t.Fatal("legacy capabilities must not watch missing paths")
	}
	encoded, err := json.Marshal(EnvironmentCapabilities{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := fields["fsWatchMissing"]; ok {
		t.Fatalf("unsupported executors must omit fsWatchMissing: %s", encoded)
	}
	encoded, err = json.Marshal(EnvironmentCapabilities{FSWatchMissing: true})
	if err != nil {
		t.Fatalf("marshal enabled: %v", err)
	}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal enabled: %v", err)
	}
	if fields["fsWatchMissing"] != true {
		t.Fatalf("enabled capabilities = %s", encoded)
	}
}

// TestFSMissingChangedInvalidatesClientWatchLikeRust covers the client-side
// observation lifecycle without a real executor connection.
func TestFSMissingChangedInvalidatesClientWatchLikeRust(t *testing.T) {
	client := &Client{}
	changed := make(chan struct{})
	client.missingWatches = map[uint64]chan struct{}{7: changed}
	if err := client.handleNotification(MethodFSMissingChanged, json.RawMessage(`{"watchId":7}`)); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("fs/missingChanged did not invalidate the observation")
	}
	client.mu.Lock()
	remaining := len(client.missingWatches)
	client.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("remaining watches = %d, want 0", remaining)
	}
	if err := client.handleNotification(MethodFSMissingChanged, json.RawMessage(`{"watchId":7}`)); err != nil {
		t.Fatalf("repeat notification: %v", err)
	}
}

// TestFSMissingChangedRequiresWatchIDLikeRust rejects malformed notifications
// instead of silently invalidating every observation.
func TestFSMissingChangedRequiresWatchIDLikeRust(t *testing.T) {
	client := &Client{}
	if err := client.handleNotification(MethodFSMissingChanged, json.RawMessage(`{}`)); err == nil {
		t.Fatal("missing watchId unexpectedly accepted")
	}
}
