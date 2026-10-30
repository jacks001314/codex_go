package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/metrics"
)

const modelsEndpointETagHeader = "X-Models-ETag"
const modelsEndpointClientVersion = "0.0.0"
const modelsEndpointRefreshTimeout = 5 * time.Second
const defaultModelsCacheTTL = 5 * time.Minute
const modelsCacheFilename = "models_cache.json"

// remoteModelsFetchUpdateDurationMetric is the remote-model fetch timer (Rust
// #46570), tagged by the authentication mode used for the fetch.
const remoteModelsFetchUpdateDurationMetric = "codex.remote_models.fetch_update.duration_ms"

type ModelsEndpoint interface {
	ListModels(ctx context.Context, etag string) (*ModelsEndpointResponse, error)
}

type ModelsEndpointResponse struct {
	Models      []ModelInfo
	ETag        string
	NotModified bool
}

// modelsEndpointHTTPError keeps a failed catalog response's HTTP status
// available to refresh policy without changing the diagnostic text.
type modelsEndpointHTTPError struct {
	StatusCode int
	Body       string
}

func (e *modelsEndpointHTTPError) Error() string {
	if e == nil {
		return "models API request failed"
	}
	if e.Body == "" {
		return fmt.Sprintf("models API request failed with status %d", e.StatusCode)
	}
	return fmt.Sprintf("models API request failed with status %d: %s", e.StatusCode, e.Body)
}

// modelsCatalogRefreshRejected reports whether a failed catalog refresh means
// the current credentials were rejected. Transient and malformed-response
// errors preserve the prior successful catalog (Rust #55958).
func modelsCatalogRefreshRejected(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *modelsEndpointHTTPError
	if errors.As(err, &statusErr) && (statusErr.StatusCode == http.StatusUnauthorized || statusErr.StatusCode == http.StatusForbidden) {
		return true
	}
	if errors.Is(err, auth.ErrGatewayLoginRequired) {
		return true
	}
	var refreshErr *auth.RefreshTokenFailedError
	return errors.As(err, &refreshErr)
}

type HTTPModelsEndpoint struct {
	Provider   *APIProvider
	Auth       *AuthHeaders
	HTTPClient HTTPDoer
	// CatalogURL, when set, is the provider's explicit Codex-native model
	// catalog (Rust `model_catalog_url`). It replaces the derived
	// `<base_url>/models` URL and switches on the bounded, no-redirect fetch.
	CatalogURL string
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
	m.apiKeyModelDiscoveryPolicy().set(enabled)
}

func (m *RemoteModelsManager) apiKeyModelDiscoveryEnabledValue() bool {
	if m == nil {
		return false
	}
	return m.apiKeyModelDiscoveryPolicy().get()
}

// SupportsAPIKeyDiscovery reports whether the current provider/auth combination
// has an authoritative catalog available with OpenAI API keys (Rust #44392).
func (m *RemoteModelsManager) SupportsAPIKeyDiscovery() bool {
	if m == nil {
		return false
	}
	return !m.apiKeyDiscoveryDisabled && m.supportsAPIKeyModels && !m.commandAuth && m.apiKeyAuth
}

// metricsAuthMode mirrors Rust's `metric_auth_mode` for the remote-model fetch
// timer (#46570): a provider API key or API-key login is "api_key", any other
// credential is "chatgpt", and an unauthenticated fetch is "none".
func (m *RemoteModelsManager) metricsAuthMode() string {
	if m == nil {
		return "none"
	}
	if m.metricsAuthModeOverride != "" {
		return m.metricsAuthModeOverride
	}
	if m.apiKeyAuth {
		return "api_key"
	}
	if m.hasAuth {
		return "chatgpt"
	}
	return "none"
}

// remoteCatalogAuthoritative reports whether a visible remote catalog should
// replace the bundled models. OpenAI API-key discovery catalogs are
// authoritative just like ChatGPT account catalogs (Rust #44392).
func (m *RemoteModelsManager) remoteCatalogAuthoritative() bool {
	if m == nil {
		return false
	}
	return m.explicitProviderCatalog || m.useRemoteCatalogAsSourceOfTruth || m.SupportsAPIKeyDiscovery()
}

func (e *HTTPModelsEndpoint) ListModels(ctx context.Context, etag string) (*ModelsEndpointResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := e.newModelsHTTPRequest(ctx, etag)
	if err != nil {
		return nil, err
	}
	catalogURL := strings.TrimSpace(e.CatalogURL)
	client := e.HTTPClient
	if client == nil {
		if catalogURL != "" {
			// Provider diagnostics may echo URL credentials, so an explicit
			// catalog never follows redirects (Rust ClientRedirectPolicy::Reject).
			client = &http.Client{
				CheckRedirect: func(*http.Request, []*http.Request) error {
					return http.ErrUseLastResponse
				},
			}
		} else {
			client = http.DefaultClient
		}
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
	// Rust #56525: an explicitly configured catalog has no response-body size
	// limit. The native response is still validated after decoding, and
	// provider diagnostics never include raw values.
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		statusErr := &modelsEndpointHTTPError{StatusCode: response.StatusCode}
		if catalogURL != "" {
			// Suppress response diagnostics that could expose credentials
			// (Rust #46561).
			return nil, statusErr
		}
		statusErr.Body = strings.TrimSpace(string(body))
		return nil, statusErr
	}
	models, err := decodeModelsEndpointResponse(body)
	if err != nil {
		return nil, err
	}
	if catalogURL != "" {
		seen := make(map[string]struct{}, len(models))
		for _, modelInfo := range models {
			slug := modelInfo.Slug
			if strings.TrimSpace(slug) == "" {
				return nil, fmt.Errorf("model catalog must contain unique, non-empty model slugs")
			}
			if _, duplicate := seen[slug]; duplicate {
				return nil, fmt.Errorf("model catalog must contain unique, non-empty model slugs")
			}
			seen[slug] = struct{}{}
		}
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
	requestURL := ""
	if catalogURL := strings.TrimSpace(e.CatalogURL); catalogURL != "" {
		catalogRequestURL, err := modelsCatalogURL(provider, catalogURL)
		if err != nil {
			return nil, err
		}
		requestURL = catalogRequestURL
	} else {
		requestURL = modelsURL(provider)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
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
	// key (Rust #44392). apiKeyAuth reports that API-key auth is in use, either
	// from an explicit provider key or an OpenAI API-key login (Rust
	// `uses_api_key_auth`). Discovery stays disabled until the feature opts in.
	supportsAPIKeyModels    bool
	apiKeyAuth              bool
	hasAuth                 bool
	metricsAuthModeOverride string
	commandAuth             bool
	// apiKeyDiscoveryDisabled mirrors Rust #46561: a custom inference base URL
	// without an explicit model_catalog_url must not opt into remote discovery.
	apiKeyDiscoveryDisabled bool
	// apiKeyModelDiscovery is shared by this manager and every originator
	// partition so a runtime policy change reaches existing catalogs (#56284).
	apiKeyModelDiscovery    *apiKeyModelDiscoveryPolicy
	explicitProviderCatalog bool
	now                     func() time.Time
	// providerModelIDs holds the slugs the provider response, or the catalog
	// loaded from its cache, advertised before merging, so catalog origins
	// survive merges, cache reloads, and empty responses (Rust #57770
	// CatalogState::provider_model_ids).
	providerModelIDs map[string]struct{}

	// initialModels is the catalog seed used when partitioning an originator.
	initialModels []ModelInfo
	// originatorManagers caches one isolated manager per supported originator
	// (Rust OpenAiModelsManager::originator_managers).
	originatorManagersMu sync.Mutex
	originatorManagers   map[ModelCatalogOriginator]*RemoteModelsManager
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
	// HasAuth reports whether any credential is available, which distinguishes
	// the "chatgpt" and "none" remote-model fetch metric modes (Rust #46570).
	HasAuth bool
	// MetricsAuthMode overrides the remote-model fetch metric's `auth_mode` tag
	// when the caller resolves the credential out of band (the TUI picker).
	// Empty derives it from APIKeyAuth and HasAuth.
	MetricsAuthMode string
	// CommandAuth reports whether the provider resolves credentials through a
	// command, which keeps the bundled API-key catalog.
	CommandAuth bool
	// DisableAPIKeyDiscovery blocks remote API-key catalog discovery while
	// leaving API-key inference and bundled models available (#46561).
	DisableAPIKeyDiscovery bool
	// ExplicitProviderCatalog marks a provider configured with model_catalog_url.
	// The provider catalog is authoritative: bundled models are not used as a
	// seed or fallback, and model metadata lookup requires an exact slug match.
	ExplicitProviderCatalog bool
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
	models := cloneModelInfos(catalog.Models)
	if options.ExplicitProviderCatalog {
		models = nil
	}
	return &RemoteModelsManager{
		remoteModels:                    models,
		endpoint:                        options.Endpoint,
		useRemoteCatalogAsSourceOfTruth: options.UseRemoteCatalogAsSourceOfTruth,
		identity:                        strings.TrimSpace(options.Identity),
		supportsAPIKeyModels:            options.SupportsAPIKeyModels,
		apiKeyAuth:                      options.APIKeyAuth,
		hasAuth:                         options.HasAuth,
		metricsAuthModeOverride:         strings.TrimSpace(options.MetricsAuthMode),
		commandAuth:                     options.CommandAuth,
		apiKeyDiscoveryDisabled:         options.DisableAPIKeyDiscovery,
		apiKeyModelDiscovery:            &apiKeyModelDiscoveryPolicy{},
		explicitProviderCatalog:         options.ExplicitProviderCatalog,
		now:                             time.Now,
		initialModels:                   cloneModelInfos(models),
		originatorManagers:              map[ModelCatalogOriginator]*RemoteModelsManager{},
	}
}

func (m *RemoteModelsManager) apiKeyModelDiscoveryPolicy() *apiKeyModelDiscoveryPolicy {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.apiKeyModelDiscovery == nil {
		m.apiKeyModelDiscovery = &apiKeyModelDiscoveryPolicy{}
	}
	return m.apiKeyModelDiscovery
}

// ForOriginator returns the cached catalog partition for a supported caller.
// A backend that cannot apply the originator header yields no partition, so it
// can never overwrite the default catalog (#56284).
func (m *RemoteModelsManager) ForOriginator(originator string) (ModelsManager, bool) {
	if m == nil {
		return nil, false
	}
	partition, ok := ParseModelCatalogOriginator(originator)
	if !ok {
		return nil, false
	}
	m.originatorManagersMu.Lock()
	defer m.originatorManagersMu.Unlock()
	if existing := m.originatorManagers[partition]; existing != nil {
		return existing, true
	}
	endpointClient, ok := m.endpoint.(OriginatorModelsEndpoint)
	if !ok {
		return nil, false
	}
	endpoint, ok := endpointClient.ForOriginator(partition)
	if !ok || endpoint == nil {
		return nil, false
	}
	m.mu.RLock()
	options := &RemoteModelsManagerOptions{
		ModelCatalog:                    &ModelsResponse{Models: cloneModelInfos(m.initialModels)},
		Endpoint:                        endpoint,
		UseRemoteCatalogAsSourceOfTruth: m.useRemoteCatalogAsSourceOfTruth,
		Identity:                        m.identity,
		SupportsAPIKeyModels:            m.supportsAPIKeyModels,
		APIKeyAuth:                      m.apiKeyAuth,
		HasAuth:                         m.hasAuth,
		MetricsAuthMode:                 m.metricsAuthModeOverride,
		CommandAuth:                     m.commandAuth,
		DisableAPIKeyDiscovery:          m.apiKeyDiscoveryDisabled,
		ExplicitProviderCatalog:         m.explicitProviderCatalog,
	}
	cachePath := m.cachePath
	cacheTTL := m.cacheTTL
	cacheClientVersion := m.cacheClientVersion
	now := m.now
	discovery := m.apiKeyModelDiscovery
	m.mu.RUnlock()
	manager := NewRemoteModelsManagerWithOptions(options)
	manager.apiKeyModelDiscovery = discovery
	manager.now = now
	if cachePath != "" {
		manager.setCachePath(modelsOriginatorCachePath(cachePath, partition), cacheTTL, cacheClientVersion)
	}
	m.originatorManagers[partition] = manager
	return manager, true
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
	cachePath := ""
	cacheTTL := time.Duration(0)
	cacheClientVersion := ""
	if codexHome != "" {
		cachePath = filepath.Join(codexHome, modelsCacheFilename)
		cacheTTL = defaultModelsCacheTTL
		cacheClientVersion = modelsEndpointClientVersion
	}
	m.setCachePath(cachePath, cacheTTL, cacheClientVersion)

	// Existing originator partitions keep their separate cache file and inherit
	// the same TTL/client-version policy as the default catalog.
	m.originatorManagersMu.Lock()
	children := make(map[ModelCatalogOriginator]*RemoteModelsManager, len(m.originatorManagers))
	for originator, manager := range m.originatorManagers {
		children[originator] = manager
	}
	m.originatorManagersMu.Unlock()
	for originator, manager := range children {
		path := ""
		if cachePath != "" {
			path = modelsOriginatorCachePath(cachePath, originator)
		}
		manager.setCachePath(path, cacheTTL, cacheClientVersion)
	}
}

func (m *RemoteModelsManager) setCachePath(cachePath string, cacheTTL time.Duration, cacheClientVersion string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.cachePath = cachePath
	m.cacheTTL = cacheTTL
	m.cacheClientVersion = cacheClientVersion
	m.mu.Unlock()
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
	models := m.remoteModels
	initialModels := m.initialModels
	apiKeyAuth := m.apiKeyAuth
	commandAuth := m.commandAuth
	discoveryDisabled := m.apiKeyDiscoveryDisabled
	supportsAPIKeyModels := m.supportsAPIKeyModels
	policy := m.apiKeyModelDiscovery
	m.mu.RUnlock()
	// With API-key discovery off, a catalog fetched while it was enabled must
	// not leak through the in-memory state (Rust `models_for_entry`). Command
	// auth keeps its existing discovery behavior.
	if apiKeyAuth && !commandAuth && (discoveryDisabled || !supportsAPIKeyModels || policy == nil || !policy.get()) {
		return cloneModelInfos(initialModels)
	}
	return cloneModelInfos(models)
}

// ModelCatalogWithOrigins mirrors Rust model_catalog_with_origins: every entry
// the provider response advertised is Provider, everything else in the merged
// catalog is Bundled.
func (m *RemoteModelsManager) ModelCatalogWithOrigins(strategy RefreshStrategy) []CatalogModel {
	if m == nil {
		return nil
	}
	m.refreshAvailableModels(strategy)
	models := m.GetRemoteModels()
	m.mu.RLock()
	providerIDs := m.providerModelIDs
	m.mu.RUnlock()
	catalog := make([]CatalogModel, 0, len(models))
	for _, info := range models {
		origin := ModelOriginBundled
		if _, ok := providerIDs[strings.TrimSpace(info.Slug)]; ok {
			origin = ModelOriginProvider
		}
		catalog = append(catalog, CatalogModel{Model: info, Origin: origin})
	}
	return catalog
}

func modelSlugSet(models []ModelInfo) map[string]struct{} {
	if len(models) == 0 {
		return nil
	}
	ids := make(map[string]struct{}, len(models))
	for _, info := range models {
		if slug := strings.TrimSpace(info.Slug); slug != "" {
			ids[slug] = struct{}{}
		}
	}
	return ids
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
	return m.GetCatalogModel(model, config).Model
}

// GetCatalogModel resolves metadata with the origin of the catalog entry it
// came from (Rust #58361): provider-advertised entries keep Provider through
// cache reloads, merged bundled entries stay Bundled, and a slug with no entry
// is Synthetic even after config overrides.
func (m *RemoteModelsManager) GetCatalogModel(model string, config *ModelsManagerConfig) CatalogModel {
	origin := m.modelOrigin(model)
	if m != nil && m.explicitProviderCatalog {
		return ConstructCatalogModelFromExactCandidate(model, m.GetRemoteModels(), origin, config)
	}
	return ConstructCatalogModelFromCandidates(model, m.GetRemoteModels(), origin, config)
}

// modelOrigin reports the catalog state that owns the requested slug.
func (m *RemoteModelsManager) modelOrigin(model string) ModelOrigin {
	if m == nil {
		return ModelOriginSynthetic
	}
	model = strings.TrimSpace(model)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if _, ok := m.providerModelIDs[model]; ok {
		return ModelOriginProvider
	}
	for _, info := range m.remoteModels {
		if strings.TrimSpace(info.Slug) == model {
			return ModelOriginBundled
		}
	}
	if candidate, ok := findModelByNamespacedSuffix(model, m.remoteModels); ok {
		if _, provider := m.providerModelIDs[strings.TrimSpace(candidate.Slug)]; provider {
			return ModelOriginProvider
		}
		return ModelOriginBundled
	}
	return ModelOriginSynthetic
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
	// Rust #57789: InMemory never loads the cache or fetches, so a catalog
	// recovered by an explicit refresh survives a failed disk-cache write.
	if strategy == RefreshInMemory {
		return
	}
	// Gate cache loading as well as requests (Rust #44392/#46561): API-key auth
	// must have an enabled, supported catalog before a remote catalog is reused,
	// so a custom base URL without `model_catalog_url` keeps the bundled models
	// even when an earlier run cached one. Command-auth providers retain their
	// existing discovery behavior.
	if m.apiKeyAuth && !m.commandAuth && (m.apiKeyDiscoveryDisabled || !m.supportsAPIKeyModels || !m.apiKeyModelDiscoveryEnabledValue()) {
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
	// Rust #46570: the remote-model fetch duration carries the authentication
	// mode used for this fetch.
	timer := metrics.StartTimer(remoteModelsFetchUpdateDurationMetric, map[string]string{
		"auth_mode": m.metricsAuthMode(),
	})
	defer timer.Stop()
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
		// A failed fetch is not an authoritative empty catalog. Keep the last
		// successful provider snapshot (with its ETag and freshness) unless the
		// access was explicitly rejected (Rust #55958).
		if err != nil && m.explicitProviderCatalog && modelsCatalogRefreshRejected(err) {
			m.mu.Lock()
			m.remoteModels = nil
			m.providerModelIDs = nil
			m.etag = ""
			m.fetched = false
			cachePath := m.cachePath
			clientVersion := m.cacheClientVersion
			identity := m.identity
			m.mu.Unlock()
			if cachePath != "" {
				_ = writeModelsCache(cachePath, &modelsCache{
					ClientVersion: clientVersion,
					Identity:      identity,
				})
			}
		}
		return
	}
	m.mu.Lock()
	m.fetched = true
	m.fetchedIdentity = m.identity
	m.providerModelIDs = modelSlugSet(response.Models)
	if len(response.Models) > 0 || m.explicitProviderCatalog {
		if m.explicitProviderCatalog || (m.remoteCatalogAuthoritative() && hasRemoteSourceOfTruthModel(response.Models)) {
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
	m.providerModelIDs = modelSlugSet(cache.Models)
	if len(cache.Models) > 0 || m.explicitProviderCatalog {
		if m.explicitProviderCatalog || (m.remoteCatalogAuthoritative() && hasRemoteSourceOfTruthModel(cache.Models)) {
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

// modelsCatalogURL builds an explicit provider catalog URL, preserving the
// provider's routing query parameters and appending the client version
// (Rust ModelsClient::catalog_request_url, #46561). Relative URLs are rejected.
func modelsCatalogURL(provider *APIProvider, catalogURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(catalogURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("model_catalog_url must be an absolute URL")
	}
	query := parsed.Query()
	if provider != nil {
		for key, value := range provider.QueryParams {
			query.Add(key, value)
		}
	}
	query.Add("client_version", modelsEndpointClientVersion)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
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
