package appserver

import (
	"strings"
	"testing"

	"codex_go/state"
)

// TestSessionModelIDsRequiresInitializedStateLikeRust pins Rust #63525: a thread
// store must provide the session alias store, and one without an initialized
// state database reports that instead of returning nothing.
func TestSessionModelIDsRequiresInitializedStateLikeRust(t *testing.T) {
	store, err := RuntimeServices{}.SessionModelIDs()
	if err == nil {
		t.Fatalf("SessionModelIDs() = %#v, want an error without state", store)
	}
	if !strings.Contains(err.Error(), "session model IDs require an initialized state database") {
		t.Fatalf("error = %v, want the Rust message", err)
	}
	if store != nil {
		t.Fatalf("SessionModelIDs() returned a store with the error: %#v", store)
	}

	// An installed alias authority wins over the state runtime, like the local
	// backend's shared store.
	memory := state.NewMemorySessionModelIDStore()
	store, err = RuntimeServices{ModelIDs: memory}.SessionModelIDs()
	if err != nil || store != memory {
		t.Fatalf("installed store = %#v, %v", store, err)
	}

	// An initialized state runtime serves as the alias authority when no other
	// store was installed.
	runtime := &state.StateRuntime{}
	store, err = RuntimeServices{StateRuntime: runtime}.SessionModelIDs()
	if err != nil || store != state.SessionModelIDStore(runtime) {
		t.Fatalf("state runtime store = %#v, %v", store, err)
	}
}
