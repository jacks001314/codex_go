package model

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const modelsEndpointETagHeader = "X-Models-ETag"
const modelsEndpointClientVersion = "0.0.0"
const modelsEndpointRefreshTimeout = 5 * time.Second
const defaultModelsCacheTTL = 5 * time.Minute
const modelsCacheFilename = "models_cache.json"

type ModelsEndpoint interface {
	ListModels(ctx context.Context, etag string) (*ModelsEndpointResponse, error)
}

type ModelsEndpointResponse struct {
	Models      []ModelInfo
	ETag        string
	NotModified bool
}

type HTTPModelsEndpoint struct {
	Provider   *APIProvider
	Auth       *AuthHeaders
	HTTPClient HTTPDoer
}

func NewHTTPModelsEndpoint(provider *APIProvider, authHeaders *AuthHeaders, httpClient HTTPDoer) *HTTPModelsEndpoint {
	return &HTTPModelsEndpoint{
		Provider:   cloneAPIProvider(provider),
		Auth:       cloneAuthHeaders(authHeaders),
		HTTPClient: httpClient,
	}
}

// SetAPIKeyModelDiscoveryEnabled records the startup API-key discovery policy
// (Rust #44392). Static catalogs ignore this setting, and live changes require
// a new session.
func (m *RemoteModelsManager) SetAPIKeyModelDiscoveryEnabled(enabled bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.apiKeyModelDiscoveryEnabled = enabled
	m.mu.Unlock()
}

func (m *RemoteModelsManager) apiKeyModelDiscoveryEnabledValue() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.apiKeyModelDiscoveryEnabled
}

// SupportsAPIKeyDiscovery reports whether the current provider/auth combination
// has an authoritative catalog available with OpenAI API keys (Rust #44392).
func (m *RemoteModelsManager) SupportsAPIKeyDiscovery() bool {
	if m == nil {
		return false
	}
	return m.supportsAPIKeyModels && !m.commandAuth && m.apiKeyAuth
}

// remoteCatalogAuthoritative reports whether a visible remote catalog should
// replace the bundled models. OpenAI API-key discovery catalogs are
// authoritative just like ChatGPT account catalogs (Rust #44392).
func (m *RemoteModelsManager) remoteCatalogAuthoritative() bool {
	if m == nil {
		return false
	}
	return m.useRemoteCatalogAsSourceOfTruth || m.SupportsAPIKeyDiscovery()
}

func (e *HTTPModelsEndpoint) ListModels(ctx context.Context, etag string) (*ModelsEndpointResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := e.newModelsHTTPRequest(ctx, etag)
	if err != nil {
		return nil, err
	}
	client := e.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return &ModelsEndpointResponse{
			ETag:        modelsETagFromHeaders(response.Header),
			NotModified: true,
		}, nil
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("models API request failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	models, err := decodeModelsEndpointResponse(body)
	if err != nil {
		return nil, err
	}
	return &ModelsEndpointResponse{
		Models: models,
		ETag:   modelsETagFromHeaders(response.Header),
	}, nil
}

func (e *HTTPModelsEndpoint) newModelsHTTPRequest(ctx context.Context, etag string) (*http.Request, error) {
	provider := e.Provider
	if provider == nil {
		provider = &APIProvider{BaseURL: defaultResponsesEndpoint}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL(provider), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if strings.TrimSpace(etag) != "" {
		request.Header.Set("If-None-Match", strings.TrimSpace(etag))
	}
	addHeaders(request.Header, provider.Headers)
	if e.Auth != nil {
		if err := e.Auth.Apply(ctx, request, nil); err != nil {
			return nil, err
		}
	}
	return request, nil
}

type RemoteModelsManager struct {
	mu                              sync.RWMutex
	remoteModels                    []ModelInfo
	etag                            string
	fetched                         bool
	endpoint                        ModelsEndpoint
	useRemoteCatalogAsSourceOfTruth bool
	cachePath                       string
	cacheTTL                        time.Duration
	cacheClientVersion              string
	// identity scopes the disk cache to the current provider and credentials
	// (Rust #43906); an empty identity disables cached catalog reuse.
	identity string
	// fetchedIdentity records the identity the in-memory catalog belongs to
	// (Rust ModelsCacheEntry::identity), so a credential change can be detected
	// before a turn samples with the previous identity's metadata (#46508).
	fetchedIdentity string
	// supportsAPIKeyModels, apiKeyAuth, and commandAuth describe whether the
	// current provider/auth combination can discover models with an OpenAI API
	// key (Rust #44392). Discovery stays disabled until the feature opts in.
	supportsAPIKeyModels        bool
	apiKeyAuth                  bool
	commandAuth                 bool
	apiKeyModelDiscoveryEnabled bool
	now                         func() time.Time
}

type modelsCache struct {
	FetchedAt     time.Time `json:"fetched_at"`
	ETag          string    `json:"etag,omitempty"`
	ClientVersion string    `json:"client_version,omitempty"`
	// Identity is the opaque provider/auth identity captured with the catalog
	// (Rust #43897). Unscoped legacy entries are cache misses.
	Identity string      `json:"identity,omitempty"`
	Models   []ModelInfo `json:"models"`
}

type RemoteModelsManagerOptions struct {
	ModelCatalog                    *ModelsResponse
	Endpoint                        ModelsEndpoint
	UseRemoteCatalogAsSourceOfTruth bool
	// Identity is the opaque provider/auth identity used to scope cached
	// catalogs. Empty disables cache reuse.
	Identity string
	// SupportsAPIKeyModels reports whether the provider serves an authoritative
	// catalog for OpenAI API keys (Rust #44392).
	SupportsAPIKeyModels bool
	// APIKeyAuth reports whether the active auth is an OpenAI API key.
	APIKeyAuth bool
	// CommandAuth reports whether the provider resolves credentials through a
	// command, which keeps the bundled API-key catalog.
	CommandAuth bool
}

func NewRemoteModelsManager(modelCatalog *ModelsResponse, endpoint ModelsEndpoint) *RemoteModelsManager {
	return NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog: modelCatalog,
		Endpoint:     endpoint,
	})
}

func NewRemoteModelsManagerWithOptions(options *RemoteModelsManagerOptions) *RemoteModelsManager {
	if options == nil {
		options = &RemoteModelsManagerOptions{}
	}
	catalog := BundledModelsResponse()
	if options.ModelCatalog != nil {
		catalog = *options.ModelCatalog
	}
	return &RemoteModelsManager{
		remoteModels:                    cloneModelInfos(catalog.Models),
		endpoint:                        options.Endpoint,
		useRemoteCatalogAsSourceOfTruth: options.UseRemoteCatalogAsSourceOfTruth,
		identity:                        strings.TrimSpace(options.Identity),
		supportsAPIKeyModels:            options.SupportsAPIKeyModels,
		apiKeyAuth:                      options.APIKeyAuth,
		commandAuth:                     options.CommandAuth,
		now:                             time.Now,
	}
}

// APIKeyModelDiscoverySetter is implemented by model managers that can toggle
// API-key model discovery (Rust #44392). Static catalogs do not implement it, so
// callers apply the policy through SetAPIKeyModelDiscoveryEnabled.
type APIKeyModelDiscoverySetter interface {
	SetAPIKeyModelDiscoveryEnabled(enabled bool)
}

// SetAPIKeyModelDiscoveryEnabled applies the startup API-key discovery policy to
// a manager when it supports it.
func SetAPIKeyModelDiscoveryEnabled(manager ModelsManager, enabled bool) {
	if setter, ok := manager.(APIKeyModelDiscoverySetter); ok {
		setter.SetAPIKeyModelDiscoveryEnabled(enabled)
	}
}

func (m *RemoteModelsManager) ConfigureCache(codexHome string) {
	if m == nil {
		return
	}
	codexHome = strings.TrimSpace(codexHome)
	m.mu.Lock()
	defer m.mu.Unlock()
	if codexHome == "" {
		m.cachePath = ""
		m.cacheTTL = 0
		m.cacheClientVersion = ""
		return
	}
	m.cachePath = filepath.Join(codexHome, modelsCacheFilename)
	m.cacheTTL = defaultModelsCacheTTL
	m.cacheClientVersion = modelsEndpointClientVersion
}

func (m *RemoteModelsManager) ListModels(strategy RefreshStrategy) []ModelPreset {
	return BuildAvailableModels(m.RawModelCatalog(strategy).Models)
}

func (m *RemoteModelsManager) RawModelCatalog(strategy RefreshStrategy) ModelsResponse {
	m.refreshAvailableModels(strategy)
	return ModelsResponse{Models: m.GetRemoteModels()}
}

func (m *RemoteModelsManager) GetRemoteModels() []ModelInfo {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneModelInfos(m.remoteModels)
}

func (m *RemoteModelsManager) GetDefaultModel(model string, allowProviderModelFallback bool, strategy RefreshStrategy) string {
	availableModels := m.ListModels(strategy)
	if allowProviderModelFallback {
		if requestedModelIsAvailable(model, availableModels) {
			return model
		}
		return defaultModelFromAvailable(availableModels)
	}
	if model != "" {
		return model
	}
	return defaultModelFromAvailable(availableModels)
}

func (m *RemoteModelsManager) GetModelInfo(model string, config *ModelsManagerConfig) ModelInfo {
	return ConstructModelInfoFromCandidates(model, m.GetRemoteModels(), config)
}

func (m *RemoteModelsManager) RefreshIfNewETag(etag string) {
	if m == nil {
		return
	}
	etag = strings.TrimSpace(etag)
	if etag == "" {
		return
	}
	m.mu.RLock()
	current := m.etag
	m.mu.RUnlock()
	if current != "" && current == etag {
		m.renewCacheTTLIfNeeded()
		return
	}
	m.refreshAvailableModels(RefreshOnline)
}

// CatalogIdentity returns the provider/auth identity the in-memory catalog
// belongs to (Rust #46508): the identity recorded when the catalog was fetched,
// or the configured identity before any fetch.
func (m *RemoteModelsManager) CatalogIdentity() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.fetched && m.fetchedIdentity != "" {
		return m.fetchedIdentity
	}
	return m.identity
}

// RefreshAfterAuthChange mirrors Rust ModelsManager::refresh_after_auth_change
// (#46508): a best-effort catalog refresh when the in-memory catalog belongs to
// different credentials, so a turn after a credential change does not sample
// with the previous identity's model metadata. The refresh runs under the same
// five-second deadline that bounds a catalog fetch, and any failure leaves the
// existing cache or the bundled fallback in place.
func (m *RemoteModelsManager) RefreshAfterAuthChange() {
	if m == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if !m.shouldRefreshModels() {
			return
		}
		m.mu.RLock()
		identity := m.identity
		fetchedIdentity := m.fetchedIdentity
		fetched := m.fetched
		m.mu.RUnlock()
		// An unscoped catalog cannot be compared to credentials, and a catalog
		// that already belongs to the current identity needs no refresh.
		if identity != "" && fetched && fetchedIdentity == identity {
			return
		}
		m.refreshAvailableModels(RefreshOnlineIfUncached)
	}()
	select {
	case <-done:
	case <-time.After(modelsEndpointRefreshTimeout):
	}
}

// shouldRefreshModels mirrors Rust's gate: a Codex-backend catalog, command
// auth, or API-key discovery all keep a catalog whose owning credentials can
// change. Go reports the Codex-backend case through the authoritative ChatGPT
// catalog flag it already tracks.
func (m *RemoteModelsManager) shouldRefreshModels() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	codexBackend := m.useRemoteCatalogAsSourceOfTruth
	commandAuth := m.commandAuth
	m.mu.RUnlock()
	return codexBackend || commandAuth || m.SupportsAPIKeyDiscovery()
}

func (m *RemoteModelsManager) refreshAvailableModels(strategy RefreshStrategy) {
	if m == nil || m.endpoint == nil {
		return
	}
	// Gate cache loading as well as requests: a session whose API-key discovery
	// is disabled keeps the bundled models (Rust #44392).
	if m.SupportsAPIKeyDiscovery() && !m.apiKeyModelDiscoveryEnabledValue() {
		return
	}
	switch strategy {
	case RefreshOnline:
		m.fetchAndUpdateModels()
	case RefreshOffline:
		m.tryLoadFreshCache()
	case RefreshOnlineIfUncached:
		m.mu.RLock()
		shouldFetch := !m.fetched
		m.mu.RUnlock()
		if shouldFetch && !m.tryLoadFreshCache() {
			m.fetchAndUpdateModels()
		}
	}
}

func (m *RemoteModelsManager) fetchAndUpdateModels() {
	m.mu.RLock()
	etag := m.etag
	m.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), modelsEndpointRefreshTimeout)
	defer cancel()
	response, err := m.endpoint.ListModels(ctx, etag)
	if err != nil || response == nil || response.NotModified {
		if response != nil && response.NotModified {
			m.mu.Lock()
			m.fetched = true
			m.fetchedIdentity = m.identity
			if strings.TrimSpace(response.ETag) != "" {
				m.etag = strings.TrimSpace(response.ETag)
			}
			m.mu.Unlock()
			m.renewCacheTTLIfNeeded()
		}
		return
	}
	m.mu.Lock()
	m.fetched = true
	m.fetchedIdentity = m.identity
	if len(response.Models) > 0 {
		if m.remoteCatalogAuthoritative() && hasRemoteSourceOfTruthModel(response.Models) {
			m.remoteModels = cloneModelInfos(response.Models)
		} else {
			m.remoteModels = mergeModelInfos(m.remoteModels, response.Models)
		}
	}
	if strings.TrimSpace(response.ETag) != "" {
		m.etag = strings.TrimSpace(response.ETag)
	}
	cache := modelsCache{
		FetchedAt:     m.nowLocked(),
		ETag:          m.etag,
		ClientVersion: m.cacheClientVersion,
		Identity:      m.identity,
		Models:        cloneModelInfos(response.Models),
	}
	cachePath := m.cachePath
	m.mu.Unlock()
	if cachePath != "" {
		_ = writeModelsCache(cachePath, &cache)
	}
}

func (m *RemoteModelsManager) tryLoadFreshCache() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	cachePath := m.cachePath
	cacheTTL := m.cacheTTL
	clientVersion := m.cacheClientVersion
	identity := m.identity
	now := m.nowLocked()
	m.mu.RUnlock()
	if cachePath == "" || cacheTTL <= 0 {
		return false
	}
	cache, err := readModelsCache(cachePath)
	if err != nil || cache.ClientVersion != clientVersion || !cache.isFresh(now, cacheTTL) {
		return false
	}
	// Cached catalogs are scoped to the current provider/auth identity; unscoped
	// legacy entries and mismatched identities are cache misses (Rust #43906).
	if identity == "" || cache.Identity != identity {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fetched {
		return true
	}
	m.fetched = true
	m.etag = strings.TrimSpace(cache.ETag)
	m.fetchedIdentity = m.identity
	if len(cache.Models) > 0 {
		if m.remoteCatalogAuthoritative() && hasRemoteSourceOfTruthModel(cache.Models) {
			m.remoteModels = cloneModelInfos(cache.Models)
		} else {
			m.remoteModels = mergeModelInfos(m.remoteModels, cache.Models)
		}
	}
	return true
}

func (m *RemoteModelsManager) renewCacheTTLIfNeeded() {
	if m == nil {
		return
	}
	m.mu.RLock()
	cachePath := m.cachePath
	cacheTTL := m.cacheTTL
	clientVersion := m.cacheClientVersion
	identity := m.identity
	etag := m.etag
	now := m.nowLocked()
	m.mu.RUnlock()
	if cachePath == "" || cacheTTL <= 0 {
		return
	}
	cache, err := readModelsCache(cachePath)
	if err != nil || cache.isFresh(now, cacheTTL/2) {
		return
	}
	// Only extend freshness when the stored version, identity, and ETag still
	// match (Rust #43906 refresh_ttl).
	if identity == "" || cache.Identity != identity || cache.ClientVersion != clientVersion || strings.TrimSpace(cache.ETag) != strings.TrimSpace(etag) {
		return
	}
	cache.FetchedAt = now
	_ = writeModelsCache(cachePath, cache)
}

func (m *RemoteModelsManager) nowLocked() time.Time {
	if m != nil && m.now != nil {
		return m.now().UTC()
	}
	return time.Now().UTC()
}

func (c *modelsCache) isFresh(now time.Time, ttl time.Duration) bool {
	if c == nil || c.FetchedAt.IsZero() || ttl <= 0 {
		return false
	}
	age := now.Sub(c.FetchedAt)
	return age <= ttl
}

func readModelsCache(path string) (*modelsCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cache modelsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	return &cache, nil
}

func writeModelsCache(path string, cache *modelsCache) error {
	if cache == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func hasRemoteSourceOfTruthModel(models []ModelInfo) bool {
	for _, model := range models {
		if strings.EqualFold(strings.TrimSpace(model.Visibility), VisibilityList) {
			return true
		}
	}
	return false
}

func modelsURL(provider *APIProvider) string {
	baseURL := strings.TrimRight(provider.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultResponsesEndpoint
	}
	raw := baseURL + "/models"
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := parsed.Query()
	if query.Get("client_version") == "" {
		query.Set("client_version", modelsEndpointClientVersion)
	}
	for key, value := range provider.QueryParams {
		query.Set(key, value)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func modelsETagFromHeaders(headers http.Header) string {
	if value := strings.TrimSpace(headers.Get(modelsEndpointETagHeader)); value != "" {
		return value
	}
	return strings.TrimSpace(headers.Get("ETag"))
}

func decodeModelsEndpointResponse(data []byte) ([]ModelInfo, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err == nil {
		if rawModels, ok := object["models"]; ok {
			var models []ModelInfo
			if err := json.Unmarshal(rawModels, &models); err != nil {
				return nil, err
			}
			return models, nil
		}
		if rawData, ok := object["data"]; ok {
			var models []ModelInfo
			if err := json.Unmarshal(rawData, &models); err != nil {
				return nil, err
			}
			return models, nil
		}
	}
	var models []ModelInfo
	if err := json.Unmarshal(data, &models); err == nil {
		return models, nil
	}
	return nil, fmt.Errorf("models API response did not contain models")
}
