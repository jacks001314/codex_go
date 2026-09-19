package plugin

import (
	"context"
	"sort"
	"strings"
)

// PluginIdentityKind distinguishes a plugin-service-assigned identity from a
// local `name@marketplace` config key.
type PluginIdentityKind string

const (
	// PluginIdentityRemote is a stable ID assigned by plugin-service and shared
	// by its materialized installations.
	PluginIdentityRemote PluginIdentityKind = "remote"
	// PluginIdentityLocal is the local `name@marketplace` config key for a
	// plugin without a remote ID.
	PluginIdentityLocal PluginIdentityKind = "local"
)

// PluginIdentity is the stable key used to merge discoveries of the same
// logical plugin (Rust `PluginIdentity`). Identity is independent of whether the
// plugin is supplied by the cloud or an executor.
type PluginIdentity struct {
	Kind PluginIdentityKind
	// RemotePluginID is set for PluginIdentityRemote.
	RemotePluginID string
	// PluginID is the local `name@marketplace` key, set for PluginIdentityLocal.
	PluginID string
}

// AsString returns the opaque identity string used for merging and lookups.
func (i PluginIdentity) AsString() string {
	if i.Kind == PluginIdentityRemote {
		return strings.TrimSpace(i.RemotePluginID)
	}
	return strings.TrimSpace(i.PluginID)
}

// PluginSourceLocationKind distinguishes the cloud and executor source variants.
type PluginSourceLocationKind string

const (
	// PluginSourceCloud is an opaque cloud resource/bundle URI, not a local path.
	PluginSourceCloud PluginSourceLocationKind = "cloud"
	// PluginSourceExecutor is a local installation owned by an executor.
	PluginSourceExecutor PluginSourceLocationKind = "executor"
)

// PluginSourceLocation is a source that can supply a plugin (Rust
// `PluginSourceLocation`).
type PluginSourceLocation struct {
	Kind PluginSourceLocationKind
	// Cloud fields; ResourceURI is opaque.
	ResourceURI string
	BundleURI   string
	// Executor fields: the environment that owns Root and the local
	// `name@marketplace` key used for this installation in configuration.
	EnvironmentID string
	PluginID      string
	Root          string
}

// PluginCatalogEntry is one plugin's manifest metadata and every location that
// can supply the same logical plugin (Rust `PluginCatalogEntry`).
type PluginCatalogEntry struct {
	ID           PluginIdentity
	DisplayName  string
	Version      string
	MCPServers   map[string]string
	ConnectorIDs []string
	Locations    []PluginSourceLocation
}

// String mirrors Rust's hand-written Debug for PluginCatalogEntry: declaration
// values can contain secrets, so only the authored server names are rendered.
func (e PluginCatalogEntry) String() string {
	names := make([]string, 0, len(e.MCPServers))
	for name := range e.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return "PluginCatalogEntry{id: " + e.ID.AsString() +
		", display_name: " + e.DisplayName +
		", version: " + e.Version +
		", mcp_server_names: [" + strings.Join(names, " ") + "]" +
		", connector_ids: [" + strings.Join(e.ConnectorIDs, " ") + "]}"
}

// PluginCatalog is one source's complete discovery snapshot (Rust
// `PluginCatalog`); no entries means no plugins were found.
type PluginCatalog struct {
	Entries  []PluginCatalogEntry
	Warnings []string
}

// PluginListQuery carries the discovery request context. The MCP extension adds
// an optional MCP resource client to this query.
type PluginListQuery struct {
	ThreadID string
	TurnID   string
}

// PluginCatalogProvider is implemented by providers that discover plugins in
// batch (Rust `PluginProvider::list`). Executor providers implement
// PluginProvider.Resolve instead; the other operation is a no-op.
type PluginCatalogProvider interface {
	ListPlugins(ctx context.Context, query PluginListQuery) (*PluginCatalog, error)
}

// ListPluginCatalog invokes a provider's batch listing when it supports one and
// otherwise returns an empty snapshot, mirroring Rust's default
// `PluginProvider::list` implementation. Rust gives both trait methods defaults,
// so a cloud provider may implement only `list`; Go therefore accepts any
// provider value and type-asserts the batch-listing method.
func ListPluginCatalog(ctx context.Context, provider any, query PluginListQuery) (*PluginCatalog, error) {
	if catalogProvider, ok := provider.(PluginCatalogProvider); ok {
		catalog, err := catalogProvider.ListPlugins(ctx, query)
		if err != nil {
			return nil, err
		}
		if catalog == nil {
			return &PluginCatalog{}, nil
		}
		return catalog, nil
	}
	return &PluginCatalog{}, nil
}
