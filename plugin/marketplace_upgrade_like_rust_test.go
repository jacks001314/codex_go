package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func configuredGitMarketplaceValues(source string) map[string]any {
	return map[string]any{"marketplaces": map[string]any{
		"system-market": map[string]any{
			"source_type":  "git",
			"source":       source,
			"ref":          "main",
			"sparse_paths": []any{"plugins/a"},
		},
		"local-market": map[string]any{"source": "/tmp/local", "source_type": "local"},
	}}
}

// TestUpgradeConfiguredMarketplacesUsesMergedConfigLikeRust covers Rust #42149:
// Git marketplaces declared by a non-user configuration layer are downloaded
// from the merged source/ref/sparse settings, and an unchanged revision is
// skipped on the next pass.
func TestUpgradeConfiguredMarketplacesUsesMergedConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	materializer := &recordingMarketplaceMaterializer{write: func(destination string) error {
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, "marker.txt"), []byte("merged"), 0o600)
	}}
	service.SetMarketplaceMaterializer(materializer)
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(source *ParsedMarketplaceSource) (string, error) {
		if source.URL != "https://example.com/system.git" || stringPtrValue(source.RefName) != "main" {
			return "", errors.New("unexpected source")
		}
		return "rev-merged", nil
	}))
	values := configuredGitMarketplaceValues("https://example.com/system.git")
	reload := func() (map[string]any, error) { return values, nil }

	outcome, err := service.UpgradeConfiguredMarketplaces(values, "", reload)
	if err != nil {
		t.Fatalf("UpgradeConfiguredMarketplaces() error = %v", err)
	}
	if len(outcome.Errors) != 0 {
		t.Fatalf("errors = %#v", outcome.Errors)
	}
	if len(outcome.SelectedMarketplaces) != 1 || outcome.SelectedMarketplaces[0] != "system-market" {
		t.Fatalf("selected = %#v", outcome.SelectedMarketplaces)
	}
	root := filepath.Join(home, InstalledMarketplacesDir, "system-market")
	if len(outcome.UpgradedRoots) != 1 || outcome.UpgradedRoots[0] != root {
		t.Fatalf("roots = %#v, want %q", outcome.UpgradedRoots, root)
	}
	if got := installedMarketplaceRevision(root); got != "rev-merged" {
		t.Fatalf("installed revision = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "marker.txt")); err != nil {
		t.Fatalf("materialized marker missing: %v", err)
	}
	if len(materializer.destinations) != 1 || !strings.Contains(materializer.destinations[0], marketplaceStagingDirName) {
		t.Fatalf("materialized into %#v, want a staging path", materializer.destinations)
	}

	outcome, err = service.UpgradeConfiguredMarketplaces(values, "", reload)
	if err != nil {
		t.Fatalf("UpgradeConfiguredMarketplaces(second) error = %v", err)
	}
	if len(outcome.UpgradedRoots) != 0 || len(materializer.destinations) != 1 {
		t.Fatalf("unchanged revision upgraded again: roots=%#v destinations=%#v", outcome.UpgradedRoots, materializer.destinations)
	}
}

// TestUpgradeConfiguredMarketplacesRollsBackOnConfigChangeLikeRust covers the
// activation check: a marketplace definition that changed during the download,
// or a configuration that can no longer be loaded, leaves the previous snapshot
// in place.
func TestUpgradeConfiguredMarketplacesRollsBackOnConfigChangeLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceMaterializer(&recordingMarketplaceMaterializer{write: func(destination string) error {
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(destination, "marker.txt"), []byte("new"), 0o600)
	}})
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(*ParsedMarketplaceSource) (string, error) {
		return "rev-new", nil
	}))
	values := configuredGitMarketplaceValues("https://example.com/system.git")
	root := filepath.Join(home, InstalledMarketplacesDir, "system-market")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		reload MarketplaceConfigReload
		want   string
	}{
		{
			name: "changed",
			reload: func() (map[string]any, error) {
				return configuredGitMarketplaceValues("https://example.com/other.git"), nil
			},
			want: "changed while auto-upgrade was in flight",
		},
		{
			name: "removed",
			reload: func() (map[string]any, error) {
				return map[string]any{"marketplaces": map[string]any{}}, nil
			},
			want: "was removed or is no longer a Git marketplace",
		},
		{
			name: "reload failure",
			reload: func() (map[string]any, error) {
				return nil, errors.New("invalid user config")
			},
			want: "failed to reload config while checking marketplace upgrade",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome, err := service.UpgradeConfiguredMarketplaces(values, "", tc.reload)
			if err != nil {
				t.Fatalf("UpgradeConfiguredMarketplaces() error = %v", err)
			}
			if len(outcome.UpgradedRoots) != 0 {
				t.Fatalf("changed configuration activated a snapshot: %#v", outcome.UpgradedRoots)
			}
			if len(outcome.Errors) != 1 || !strings.Contains(outcome.Errors[0].Message, tc.want) {
				t.Fatalf("errors = %#v, want %q", outcome.Errors, tc.want)
			}
			data, err := os.ReadFile(filepath.Join(root, "marker.txt"))
			if err != nil || string(data) != "old" {
				t.Fatalf("previous snapshot = %q, %v", data, err)
			}
		})
	}
}

// TestUpgradeConfiguredMarketplacesRejectsReservedAndMissingLikeRust covers the
// validation Rust performs before installation.
func TestUpgradeConfiguredMarketplacesRejectsReservedAndMissingLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewPluginService()
	service.SetCodexHome(home)
	service.SetMarketplaceMaterializer(&recordingMarketplaceMaterializer{})
	service.SetMarketplaceRevisionResolver(MarketplaceRevisionResolverFunc(func(*ParsedMarketplaceSource) (string, error) {
		return "rev", nil
	}))
	values := map[string]any{"marketplaces": map[string]any{
		OpenAICuratedMarketplaceName: map[string]any{"source_type": "git", "source": "https://example.com/curated.git"},
		"missing-source":             map[string]any{"source_type": "git"},
	}}
	outcome, err := service.UpgradeConfiguredMarketplaces(values, "", nil)
	if err != nil {
		t.Fatalf("UpgradeConfiguredMarketplaces() error = %v", err)
	}
	if len(outcome.UpgradedRoots) != 0 {
		t.Fatalf("invalid marketplaces were upgraded: %#v", outcome.UpgradedRoots)
	}
	messages := map[string]string{}
	for _, item := range outcome.Errors {
		messages[item.MarketplaceName] = item.Message
	}
	if !strings.Contains(messages[OpenAICuratedMarketplaceName], "reserved") {
		t.Fatalf("reserved error = %#v", messages)
	}
	if !strings.Contains(messages["missing-source"], "missing source") {
		t.Fatalf("missing source error = %#v", messages)
	}

	if _, err := service.UpgradeConfiguredMarketplaces(values, "not-configured", nil); !errors.Is(err, ErrInvalidPluginRequest) {
		t.Fatalf("unnamed upgrade error = %v, want ErrInvalidPluginRequest", err)
	}
}

type recordingMarketplaceMaterializer struct {
	destinations []string
	write        func(destination string) error
}

func (m *recordingMarketplaceMaterializer) MaterializeMarketplace(_ *ParsedMarketplaceSource, _ []string, destination string) error {
	m.destinations = append(m.destinations, destination)
	if m.write != nil {
		return m.write(destination)
	}
	return nil
}
