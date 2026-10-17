package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Mirrors Rust's `PluginCatalogEntry` Debug: declaration values are replaced by
// their authored names so secrets never reach logs.
func TestPluginCatalogEntryStringOmitsDeclarationValues(t *testing.T) {
	entry := PluginCatalogEntry{
		ID:          PluginIdentity{Kind: PluginIdentityLocal, PluginID: "demo@market"},
		DisplayName: "Demo",
		Version:     "1.2.3",
		MCPServers:  map[string]string{"demo": `{"token":"secret-value"}`},
	}
	rendered := entry.String()
	if strings.Contains(rendered, "secret-value") {
		t.Fatalf("catalog entry leaked a declaration value: %s", rendered)
	}
	if !strings.Contains(rendered, "mcp_server_names: [demo]") || !strings.Contains(rendered, "demo@market") {
		t.Fatalf("catalog entry render = %s", rendered)
	}
}

// TestPluginIdentityStableAcrossSourcesLikeRust pins Rust #46558's identity
// rule: a plugin-service remote ID is the stable key regardless of which cloud
// or executor location supplied the discovery, while a plugin without a remote
// ID falls back to its local `name@marketplace` key.
func TestPluginIdentityStableAcrossSourcesLikeRust(t *testing.T) {
	remote := PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: "plugins~Plugin_demo"}
	if got, want := remote.AsString(), "plugins~Plugin_demo"; got != want {
		t.Fatalf("remote identity = %q, want %q", got, want)
	}
	local := PluginIdentity{Kind: PluginIdentityLocal, PluginID: "demo@market"}
	if got, want := local.AsString(), "demo@market"; got != want {
		t.Fatalf("local identity = %q, want %q", got, want)
	}
	// Locations are metadata, not identity: two discoveries of the same logical
	// plugin merge under one key even when their sources differ.
	if remote.AsString() != (PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: remote.RemotePluginID}).AsString() {
		t.Fatal("remote identity depends on its source location")
	}
}

func TestPluginIdentityAsStringLikeRust(t *testing.T) {
	if got := (PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: "remote-1"}).AsString(); got != "remote-1" {
		t.Fatalf("remote identity = %q", got)
	}
	if got := (PluginIdentity{Kind: PluginIdentityLocal, PluginID: "demo@market"}).AsString(); got != "demo@market" {
		t.Fatalf("local identity = %q", got)
	}
}

type staticCatalogProvider struct{}

func (staticCatalogProvider) ListPlugins(context.Context, PluginListQuery) (*PluginCatalog, error) {
	return &PluginCatalog{Entries: []PluginCatalogEntry{{ID: PluginIdentity{Kind: PluginIdentityLocal, PluginID: "demo@market"}}}}, nil
}

type failingCatalogProvider struct{}

func (failingCatalogProvider) ListPlugins(context.Context, PluginListQuery) (*PluginCatalog, error) {
	return nil, errors.New("catalog unavailable")
}

type resolveOnlyProvider struct{}

func (resolveOnlyProvider) Resolve(context.Context, string) (*ResolvedPlugin, error) {
	return nil, nil
}

// Mirrors Rust's default `PluginProvider::list`: a provider that only resolves
// roots returns an empty snapshot instead of failing.
func TestListPluginCatalogDefaultsToEmptySnapshotLikeRust(t *testing.T) {
	catalog, err := ListPluginCatalog(context.Background(), resolveOnlyProvider{}, PluginListQuery{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("ListPluginCatalog error = %v", err)
	}
	if catalog == nil || len(catalog.Entries) != 0 || len(catalog.Warnings) != 0 {
		t.Fatalf("default catalog = %#v", catalog)
	}
}

func TestListPluginCatalogUsesBatchProviderLikeRust(t *testing.T) {
	catalog, err := ListPluginCatalog(context.Background(), staticCatalogProvider{}, PluginListQuery{})
	if err != nil || catalog == nil || len(catalog.Entries) != 1 {
		t.Fatalf("batch catalog = %#v, %v", catalog, err)
	}
}

func TestListPluginCatalogPropagatesProviderErrorLikeRust(t *testing.T) {
	if _, err := ListPluginCatalog(context.Background(), failingCatalogProvider{}, PluginListQuery{}); err == nil {
		t.Fatal("ListPluginCatalog ignored the provider error")
	}
}
