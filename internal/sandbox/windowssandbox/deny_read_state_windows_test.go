//go:build windows

package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncPersistentDenyReadACLsStoresAppliedPaths(t *testing.T) {
	codexHome := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret")
	applied, err := SyncPersistentDenyReadACLs(codexHome, []string{secret}, testCapabilitySID)
	if err != nil {
		t.Fatalf("SyncPersistentDenyReadACLs() error = %v", err)
	}
	if len(applied) == 0 || !containsLexicalPath(applied, secret) {
		t.Fatalf("applied = %#v, want %q", applied, secret)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Fatalf("secret path was not materialized: %v", err)
	}
	state, err := loadDenyReadACLState(denyReadACLStatePath(codexHome))
	if err != nil {
		t.Fatalf("loadDenyReadACLState() error = %v", err)
	}
	if !containsLexicalPath(state.Principals[testCapabilitySID], secret) {
		t.Fatalf("state = %#v, want applied secret path", state)
	}
}
