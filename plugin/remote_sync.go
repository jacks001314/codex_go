package plugin

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
)

const (
	catalogCacheDir       = "cache/remote_plugin_catalog"
	catalogCacheSchemaVer = 1
)

// RemotePluginCatalog is the cached representation of a remote plugin catalog.
type RemotePluginCatalog struct {
	SchemaVersion int               `json:"schema_version"`
	Plugins       []CatalogPlugin   `json:"plugins"`
	Marketplaces  []CatalogMarketplace `json:"marketplaces"`
}

// CatalogPlugin is a plugin entry in the remote catalog.
type CatalogPlugin struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	DisplayName      string   `json:"display_name"`
	Description      string   `json:"description"`
	Status           string   `json:"status"`
	InstallationPolicy string `json:"installation_policy"`
	Release          struct {
		DisplayName string   `json:"display_name"`
		AppIDs      []string `json:"app_ids"`
	} `json:"release"`
}

// CatalogMarketplace is a marketplace entry in the remote catalog.
type CatalogMarketplace struct {
	Name      string `json:"name"`
	SourceURL string `json:"source_url"`
	SourceRef string `json:"source_ref"`
}

// CatalogCacheKey identifies a specific catalog cache entry.
type CatalogCacheKey struct {
	ChatGPTBaseURL     string `json:"chatgpt_base_url"`
	AccountID          string `json:"account_id"`
	ChatGPTUserID      string `json:"chatgpt_user_id"`
	IsWorkspaceAccount bool   `json:"is_workspace_account"`
}

// CatalogCache manages disk caching of remote plugin catalogs.
// Catalogs are cached using a content-addressed scheme based on an FNV-1a hash
// of the serialized cache key.
type CatalogCache struct {
	codexHome string
}

// NewCatalogCache creates a CatalogCache rooted at the given Codex home directory.
func NewCatalogCache(codexHome string) *CatalogCache {
	return &CatalogCache{codexHome: strings.TrimSpace(codexHome)}
}

// cacheFilePath returns the file path for a given cache key.
func (c *CatalogCache) cacheFilePath(key *CatalogCacheKey) (string, error) {
	keyJSON, err := json.Marshal(key)
	if err != nil {
		return "", fmt.Errorf("failed to serialize catalog cache key: %w", err)
	}
	h := fnv.New64a()
	h.Write(keyJSON)
	hash := fmt.Sprintf("%016x", h.Sum64())
	return filepath.Join(c.codexHome, catalogCacheDir, hash+".json"), nil
}

// Load reads a cached catalog for the given key. Returns nil if no cache exists
// or if the cache is invalid/outdated.
func (c *CatalogCache) Load(key *CatalogCacheKey) (*RemotePluginCatalog, error) {
	path, err := c.cacheFilePath(key)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read catalog cache: %w", err)
	}

	var catalog RemotePluginCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		// Invalid cache — delete and return nil
		os.Remove(path)
		return nil, nil
	}

	if catalog.SchemaVersion != catalogCacheSchemaVer {
		// Schema mismatch — delete and return nil
		os.Remove(path)
		return nil, nil
	}

	return &catalog, nil
}

// Save writes a catalog to the disk cache for the given key.
func (c *CatalogCache) Save(key *CatalogCacheKey, catalog *RemotePluginCatalog) error {
	catalog.SchemaVersion = catalogCacheSchemaVer

	path, err := c.cacheFilePath(key)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create catalog cache directory: %w", err)
	}

	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize catalog cache: %w", err)
	}
	data = append(data, '\n')

	// Write atomically
	tmp, err := os.CreateTemp(filepath.Dir(path), ".catalog-cache-")
	if err != nil {
		return fmt.Errorf("failed to create temporary catalog cache file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write catalog cache: %w", err)
	}
	tmp.Close()
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to persist catalog cache: %w", err)
	}
	return nil
}

// Delete removes the cached catalog for the given key.
func (c *CatalogCache) Delete(key *CatalogCacheKey) error {
	path, err := c.cacheFilePath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to delete catalog cache: %w", err)
	}
	return nil
}

// RemotePluginSyncResult describes the result of a remote installed plugin sync.
type RemotePluginSyncResult struct {
	Installed   []string
	Updated     []string
	Removed     []string
	Errors      []string
}

// RemoteInstalledPlugin represents a remotely installed plugin with its download URL.
type RemoteInstalledPlugin struct {
	PluginID    string
	Version     string
	DownloadURL string
}

// SyncRemoteInstalledPlugins synchronizes the local plugin store with remotely
// installed plugin bundles. It compares installed versions against cached versions,
// downloads new/updated bundles, and removes stale entries.
//
// This function is a stub for the full remote sync logic. The full implementation
// would fetch installed plugins from the backend API, download bundles, and install
// them via PluginStore. The core logic includes:
// - Fetching installed plugins for Global, Workspace, and User scopes
// - Comparing installed versions against locally cached versions
// - Downloading tar.gz bundles when versions differ
// - Installing into PluginStore via atomic install
// - Cleaning up stale cache entries not present in the remote set
// - Guarding with a mutex to prevent races with active installs/uninstalls
type RemotePluginSyncer struct {
	store       *PluginStore
	catalogCache *CatalogCache
}

// NewRemotePluginSyncer creates a new syncer.
func NewRemotePluginSyncer(store *PluginStore, catalogCache *CatalogCache) *RemotePluginSyncer {
	return &RemotePluginSyncer{
		store:        store,
		catalogCache: catalogCache,
	}
}
