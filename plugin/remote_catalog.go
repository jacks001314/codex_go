package plugin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	RemoteCatalogScopeGlobal    = "GLOBAL"
	RemoteCatalogScopeUser      = "USER"
	RemoteCatalogScopeWorkspace = "WORKSPACE"

	remoteCatalogCacheDir    = "cache/remote_plugin_catalog"
	remoteCatalogPageLimit   = 100
	remoteCatalogCacheTTL    = 10 * time.Minute
	remoteCatalogHTTPTimeout = 15 * time.Second
)

// RemoteCatalogOptions configures one remote plugin catalog request.
type RemoteCatalogOptions struct {
	BaseURL      string
	AccessToken  string
	AccountID    string
	ProductSKU   string
	Scope        string
	Collection   string
	CodexHome    string
	HTTPClient   HTTPDoer
	ForceRefresh bool
}

// RemoteCatalogRelease is the release metadata returned by the remote catalog.
type RemoteCatalogRelease struct {
	Version           *string         `json:"version"`
	DisplayName       string          `json:"display_name"`
	Description       string          `json:"description"`
	BundleDownloadURL *string         `json:"bundle_download_url"`
	AppManifest       json.RawMessage `json:"app_manifest"`
	Keywords          []string        `json:"keywords"`
}

// RemoteCatalogPlugin is one remote plugin directory entry.
type RemoteCatalogPlugin struct {
	ID                   string                `json:"id"`
	Name                 string                `json:"name"`
	Scope                string                `json:"scope"`
	InstallationPolicy   PluginInstallPolicy   `json:"installation_policy"`
	AuthenticationPolicy PluginAuthPolicy      `json:"authentication_policy"`
	Availability         PluginAvailability    `json:"status"`
	DisabledReason       *PluginDisabledReason `json:"disabled_reason"`
	EligiblePlanTypes    *[]string             `json:"eligible_plan_types"`
	Release              RemoteCatalogRelease  `json:"release"`
}

type remoteCatalogPage struct {
	Plugins    []RemoteCatalogPlugin `json:"plugins"`
	Pagination struct {
		NextPageToken *string `json:"next_page_token"`
	} `json:"pagination"`
}

type remoteCatalogCacheFile struct {
	FetchedAt time.Time             `json:"fetched_at"`
	Plugins   []RemoteCatalogPlugin `json:"plugins"`
}

// RemoteMarketplaceNameForScope maps a catalog scope to the canonical
// marketplace name Rust uses for remote plugins.
func RemoteMarketplaceNameForScope(scope string) string {
	switch strings.ToUpper(strings.TrimSpace(scope)) {
	case RemoteCatalogScopeUser:
		return "created-by-me-remote"
	case RemoteCatalogScopeWorkspace:
		return "workspace-directory"
	default:
		return RemoteGlobalMarketplaceName
	}
}

// RemoteCatalogScopeForMarketplace returns the catalog scope for a remote
// marketplace name.
func RemoteCatalogScopeForMarketplace(name string) (string, bool) {
	switch strings.TrimSpace(name) {
	case RemoteGlobalMarketplaceName:
		return RemoteCatalogScopeGlobal, true
	case "created-by-me-remote":
		return RemoteCatalogScopeUser, true
	case "workspace-directory", "workspace-shared-with-me":
		return RemoteCatalogScopeWorkspace, true
	default:
		return "", false
	}
}

// FetchRemoteCatalogPlugins lists one remote directory scope, optionally
// filtered by collection, and caches the merged pages by scope/collection.
func FetchRemoteCatalogPlugins(ctx context.Context, opts *RemoteCatalogOptions) ([]RemoteCatalogPlugin, error) {
	if opts == nil {
		return nil, fmt.Errorf("remote catalog options are required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	token := strings.TrimSpace(opts.AccessToken)
	if baseURL == "" {
		return nil, fmt.Errorf("ChatGPT base URL is required for remote plugins")
	}
	if token == "" {
		return nil, fmt.Errorf("ChatGPT auth is required to load remote plugins")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scope := strings.ToUpper(strings.TrimSpace(opts.Scope))
	if scope == "" {
		scope = RemoteCatalogScopeGlobal
	}
	if cachePath, ok := remoteCatalogCachePath(opts, baseURL, scope); ok && !opts.ForceRefresh {
		if cached, err := readRemoteCatalogCache(cachePath); err == nil && cached != nil {
			return cached, nil
		}
	}
	plugins, err := fetchRemoteCatalogPages(ctx, opts, baseURL, token, scope)
	if err != nil {
		return nil, err
	}
	if cachePath, ok := remoteCatalogCachePath(opts, baseURL, scope); ok {
		_ = writeRemoteCatalogCache(cachePath, plugins)
	}
	return plugins, nil
}

// FetchRemotePluginDetail fetches one remote plugin with its bundle download
// URL and app manifest populated.
func FetchRemotePluginDetail(ctx context.Context, opts *RemoteCatalogOptions, remotePluginID string) (*RemoteCatalogPlugin, error) {
	if opts == nil {
		return nil, fmt.Errorf("remote catalog options are required")
	}
	remotePluginID = strings.TrimSpace(remotePluginID)
	if remotePluginID == "" {
		return nil, fmt.Errorf("remote plugin id is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	token := strings.TrimSpace(opts.AccessToken)
	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("ChatGPT auth is required for remote plugins")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, remoteCatalogHTTPTimeout)
	defer cancel()
	endpoint := baseURL + "/ps/plugins/" + url.PathEscape(remotePluginID) + "?includeDownloadUrls=true"
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	if accountID := strings.TrimSpace(opts.AccountID); accountID != "" {
		request.Header.Set("ChatGPT-Account-ID", accountID)
	}
	sku := strings.TrimSpace(opts.ProductSKU)
	if sku == "" {
		sku = CODEXProductSKU
	}
	request.Header.Set("OAI-Product-Sku", sku)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch remote plugin detail: %w", err)
	}
	defer response.Body.Close()
	var detail RemoteCatalogPlugin
	decodeErr := json.NewDecoder(response.Body).Decode(&detail)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("remote plugin detail request failed with status %d", response.StatusCode)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("decode remote plugin detail: %w", decodeErr)
	}
	if detail.ID != "" && detail.ID != remotePluginID {
		return nil, fmt.Errorf("remote plugin detail returned id %q, want %q", detail.ID, remotePluginID)
	}
	return &detail, nil
}

func fetchRemoteCatalogPages(ctx context.Context, opts *RemoteCatalogOptions, baseURL, token, scope string) ([]RemoteCatalogPlugin, error) {
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	var plugins []RemoteCatalogPlugin
	pageToken := ""
	for {
		endpoint, err := url.Parse(baseURL + "/ps/plugins/list")
		if err != nil {
			return nil, fmt.Errorf("parse remote plugin catalog endpoint: %w", err)
		}
		query := endpoint.Query()
		query.Set("scope", scope)
		query.Set("limit", fmt.Sprintf("%d", remoteCatalogPageLimit))
		if collection := strings.TrimSpace(opts.Collection); collection != "" {
			query.Set("collection", collection)
		}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		endpoint.RawQuery = query.Encode()
		requestCtx, cancel := context.WithTimeout(ctx, remoteCatalogHTTPTimeout)
		request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			cancel()
			return nil, err
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("Authorization", "Bearer "+token)
		if accountID := strings.TrimSpace(opts.AccountID); accountID != "" {
			request.Header.Set("ChatGPT-Account-ID", accountID)
		}
		sku := strings.TrimSpace(opts.ProductSKU)
		if sku == "" {
			sku = CODEXProductSKU
		}
		request.Header.Set("OAI-Product-Sku", sku)
		response, err := client.Do(request)
		if err != nil {
			cancel()
			return nil, fmt.Errorf("fetch remote plugin catalog: %w", err)
		}
		var page remoteCatalogPage
		decodeErr := json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		cancel()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("remote plugin catalog request failed with status %d", response.StatusCode)
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decode remote plugin catalog: %w", decodeErr)
		}
		plugins = append(plugins, page.Plugins...)
		if page.Pagination.NextPageToken == nil || strings.TrimSpace(*page.Pagination.NextPageToken) == "" {
			return plugins, nil
		}
		pageToken = strings.TrimSpace(*page.Pagination.NextPageToken)
	}
}

func remoteCatalogCachePath(opts *RemoteCatalogOptions, baseURL, scope string) (string, bool) {
	home := strings.TrimSpace(opts.CodexHome)
	if home == "" {
		return "", false
	}
	key := strings.Join([]string{
		baseURL,
		strings.TrimSpace(opts.AccountID),
		scope,
		strings.TrimSpace(opts.Collection),
	}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(home, remoteCatalogCacheDir, hex.EncodeToString(sum[:])+".json"), true
}

func readRemoteCatalogCache(path string) ([]RemoteCatalogPlugin, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if time.Since(info.ModTime()) > remoteCatalogCacheTTL {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cached remoteCatalogCacheFile
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}
	return cached.Plugins, nil
}

func writeRemoteCatalogCache(path string, plugins []RemoteCatalogPlugin) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(remoteCatalogCacheFile{FetchedAt: time.Now().UTC(), Plugins: plugins}, "", "  ")
	if err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

// RemotePluginMutation performs the install/uninstall mutation for one remote
// plugin id. Install returns the plugin ids whose apps still need auth.
func InstallRemotePluginMutation(ctx context.Context, opts *RemoteCatalogOptions, remotePluginID string) ([]string, error) {
	return remotePluginMutation(ctx, opts, remotePluginID, "install", true)
}

// UninstallRemotePluginMutation disables one remote plugin server-side.
func UninstallRemotePluginMutation(ctx context.Context, opts *RemoteCatalogOptions, remotePluginID string) error {
	_, err := remotePluginMutation(ctx, opts, remotePluginID, "uninstall", false)
	return err
}

func remotePluginMutation(ctx context.Context, opts *RemoteCatalogOptions, remotePluginID, action string, wantEnabled bool) ([]string, error) {
	if opts == nil {
		return nil, fmt.Errorf("remote catalog options are required")
	}
	remotePluginID = strings.TrimSpace(remotePluginID)
	if remotePluginID == "" {
		return nil, fmt.Errorf("remote plugin id is required")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	token := strings.TrimSpace(opts.AccessToken)
	if baseURL == "" || token == "" {
		return nil, fmt.Errorf("ChatGPT auth is required for remote plugins")
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := baseURL + "/ps/plugins/" + url.PathEscape(remotePluginID) + "/" + action
	if action == "install" {
		endpoint += "?includeAppsNeedingAuth=true"
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, remoteCatalogHTTPTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	if accountID := strings.TrimSpace(opts.AccountID); accountID != "" {
		request.Header.Set("ChatGPT-Account-ID", accountID)
	}
	sku := strings.TrimSpace(opts.ProductSKU)
	if sku == "" {
		sku = CODEXProductSKU
	}
	request.Header.Set("OAI-Product-Sku", sku)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("remote plugin %s failed: %w", action, err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("remote plugin %s failed with status %d: %s", action, response.StatusCode, strings.TrimSpace(string(body)))
	}
	if readErr != nil {
		return nil, readErr
	}
	var mutation struct {
		ID                string   `json:"id"`
		Enabled           bool     `json:"enabled"`
		AppIDsNeedingAuth []string `json:"app_ids_needing_auth"`
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &mutation); err != nil {
			return nil, fmt.Errorf("decode remote plugin %s response: %w", action, err)
		}
		if mutation.ID != "" && mutation.ID != remotePluginID {
			return nil, fmt.Errorf("remote plugin %s returned id %q, want %q", action, mutation.ID, remotePluginID)
		}
		if mutation.Enabled != wantEnabled {
			return nil, fmt.Errorf("remote plugin %s returned enabled=%v", action, mutation.Enabled)
		}
	}
	return mutation.AppIDsNeedingAuth, nil
}
