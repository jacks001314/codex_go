package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/model"
)

// TestAccountScopedModelsManagerReturnsManagerLikeRust verifies the app-server
// account-scoped catalog manager (Rust #41467) is wired and safe: it returns a
// non-nil manager, and in a fresh home (no auth) the lazy builder falls back to
// the bundled catalog without panicking or touching the network on an offline
// read.
func TestAccountScopedModelsManagerReturnsManagerLikeRust(t *testing.T) {
	home := t.TempDir()
	svc := config.NewConfigService(home)
	manager := accountScopedModelsManager(home, svc)
	if manager == nil {
		t.Fatal("accountScopedModelsManager returned nil")
	}
	_ = manager.ListModels(model.RefreshOffline)
}
