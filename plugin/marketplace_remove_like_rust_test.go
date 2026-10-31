package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveMarketplaceUnknownMatchesRust pins Rust's rejection of a
// marketplace that is neither configured nor installed: the user config has no
// entry and no installed root exists.
func TestRemoveMarketplaceUnknownMatchesRust(t *testing.T) {
	service := NewPluginService()
	service.SetCodexHome(t.TempDir())
	if _, err := service.RemoveMarketplace(&MarketplaceRemoveParams{MarketplaceName: "nosuch"}); err == nil {
		t.Fatal("RemoveMarketplace(nosuch) error = nil")
	} else {
		if !errors.Is(err, ErrInvalidPluginRequest) {
			t.Fatalf("error = %v, want ErrInvalidPluginRequest", err)
		}
		want := "invalid plugin request: marketplace `nosuch` is not configured or installed"
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err.Error(), want)
		}
	}
}

// TestRemoveMarketplaceDeletesGitCloneLikeRust pins the removal of the cloned
// marketplace root; a local marketplace keeps pointing at the user's own
// directory and must never be deleted.
func TestRemoveMarketplaceDeletesGitCloneLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceMaterializer(fakeMarketplaceMaterializer(t))
	if _, err := service.AddMarketplace(&MarketplaceAddParams{Name: "debug", Source: "owner/repo"}); err != nil {
		t.Fatalf("AddMarketplace(git) error = %v", err)
	}
	clone := filepath.Join(home, InstalledMarketplacesDir, "debug")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatalf("MkdirAll clone error = %v", err)
	}
	if _, err := service.RemoveMarketplace(&MarketplaceRemoveParams{MarketplaceName: "debug"}); err != nil {
		t.Fatalf("RemoveMarketplace(debug) error = %v", err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatalf("clone still exists, stat error = %v", err)
	}
}
