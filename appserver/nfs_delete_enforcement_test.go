package appserver

import (
	"context"
	"testing"
)

// TestResolveDefaultStateRuntimeAppliesNFSDeleteEnforcementLikeRust mirrors Rust
// #57225: the managed `unsafe_enforce_sqlite_journal_mode` requirement reaches
// the SqliteConfig that opens the state runtime, and stays off by default.
func TestResolveDefaultStateRuntimeAppliesNFSDeleteEnforcementLikeRust(t *testing.T) {
	for _, enforce := range []bool{false, true} {
		runtime, owned, err := resolveDefaultStateRuntime(context.Background(), t.TempDir(), nil, "", enforce)
		if err != nil {
			t.Fatalf("resolveDefaultStateRuntime(enforce=%v): %v", enforce, err)
		}
		if !owned || runtime == nil {
			t.Fatalf("resolveDefaultStateRuntime(enforce=%v) did not return an owned runtime", enforce)
		}
		if got := runtime.SQLite().EnforcesNFSDelete(); got != enforce {
			t.Fatalf("EnforcesNFSDelete() = %v, want %v", got, enforce)
		}
		if err := runtime.Close(); err != nil {
			t.Fatalf("close runtime: %v", err)
		}
	}
}
