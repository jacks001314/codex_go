package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomaticMarketplaceUpgradeCachesNameMismatchLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	firstWrites := 0
	service.SetMarketplaceMaterializer(&recordingMarketplaceMaterializer{write: func(destination string) error {
		firstWrites++
		if err := os.MkdirAll(filepath.Join(destination, ".agents", "plugins"), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, ".agents", "plugins", "marketplace.json"), []byte(`{"name":"other-market"}`), 0o600)
	}})
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(*ParsedMarketplaceSource) (string, error) {
		return "rev-mismatch", nil
	}))
	values := configuredGitMarketplaceValues("https://example.com/system.git")
	reload := func() (map[string]any, error) { return values, nil }

	outcome, err := service.upgradeConfiguredMarketplaces(values, "", reload, true)
	if err != nil {
		t.Fatalf("first automatic upgrade error = %v", err)
	}
	if firstWrites != 1 || len(outcome.Errors) != 1 {
		t.Fatalf("first upgrade writes=%d errors=%#v", firstWrites, outcome.Errors)
	}
	if !strings.Contains(outcome.Errors[0].Message, "does not match configured marketplace") {
		t.Fatalf("mismatch message = %q", outcome.Errors[0].Message)
	}
	cachePath := marketplaceNameMismatchCachePath(home, "system-market")
	body, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("mismatch cache missing: %v", err)
	}
	if bytes.Contains(body, []byte("https://example.com/system.git")) {
		t.Fatalf("cache persisted the source URL: %s", body)
	}

	secondWrites := 0
	service.SetMarketplaceMaterializer(&recordingMarketplaceMaterializer{write: func(string) error {
		secondWrites++
		return nil
	}})
	outcome, err = service.upgradeConfiguredMarketplaces(values, "", reload, true)
	if err != nil {
		t.Fatalf("second automatic upgrade error = %v", err)
	}
	if secondWrites != 0 || len(outcome.Errors) != 1 {
		t.Fatalf("cached mismatch materialized again: writes=%d errors=%#v", secondWrites, outcome.Errors)
	}
}
