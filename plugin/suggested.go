package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"codex_go/tracehook"
)

const RemoteGlobalMarketplaceName = "openai-curated-remote"

// Info-level recommendation spans (Rust #62326). Rust names the whole
// recommendation load plus its two remote reads so a slow recommendation
// endpoint is visible in the initial-context trace.
const (
	RecommendationsLoadSpanName         = "plugins.recommendations.load"
	RecommendationsFetchCatalogSpanName = "plugins.recommendations.fetch_catalog"
)

// startRecommendationSpan opens one recommendation span under the span the
// caller already carries, if any. A context without tracing stays unchanged.
func startRecommendationSpan(ctx context.Context, name string) tracehook.Span {
	parent := tracehook.SpanFromContext(ctx)
	if parent == nil {
		return nil
	}
	return parent.StartChildSpan(name, nil)
}

const suggestedPluginsTimeout = 5 * time.Second

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type SuggestedPluginProvider interface {
	ListSuggestedPlugins(ctx context.Context) (*SuggestedPluginList, error)
}

// SuggestedPluginServiceScope identifies the plugin service endpoint and
// account a recommendation catalog was fetched for (Rust #62610
// PluginServiceScope). Invalidation compares this scope instead of polling the
// endpoint for a tooling marker.
type SuggestedPluginServiceScope struct {
	ChatGPTBaseURL string
	AccountID      string
}

// SuggestedPluginScopeProvider reports the service scope its recommendation
// catalog belongs to. Providers without it are treated as unscoped, and an
// invalidation for any scope refreshes them.
type SuggestedPluginScopeProvider interface {
	SuggestedPluginServiceScope() SuggestedPluginServiceScope
}

type SuggestedPluginList struct {
	Enabled bool
	Plugins []SuggestedPlugin
}

type suggestedPluginFetch struct {
	done chan struct{}
	list *SuggestedPluginList
	err  error
}

type SuggestedPlugin struct {
	ID              string
	RemotePluginID  string
	Name            string
	DisplayName     string
	AppConnectorIDs []string
}

type HTTPSuggestedPluginProvider struct {
	BaseURL     string
	AccessToken string
	AccountID   string
	HTTPClient  HTTPDoer
	// ProductSKU is the value stamped on OAI-Product-Sku; empty falls back to
	// CODEXProductSKU (Rust RemotePluginServiceConfig.product_sku, #49100).
	ProductSKU string
	// Pool is the owner's shared connection pool. It is used whenever
	// HTTPClient is nil, so repeated service configurations reuse connections
	// (Rust #49100).
	Pool *RemotePluginHTTPPool
	// RuntimeAuth resolves runtime request credentials per request when runtime
	// agent instances are enabled (Rust #59564).
	RuntimeAuth RuntimeAuthProvider
}

func NewHTTPSuggestedPluginProvider(baseURL string, accessToken string, accountID string, client HTTPDoer) *HTTPSuggestedPluginProvider {
	return NewHTTPSuggestedPluginProviderWithPool(baseURL, accessToken, accountID, client, nil)
}

// NewHTTPSuggestedPluginProviderWithPool builds a remote plugin service
// configuration that shares the owner's connection pool (Rust #49100,
// PluginsConfigInput::remote_plugin_service_config).
func NewHTTPSuggestedPluginProviderWithPool(baseURL string, accessToken string, accountID string, client HTTPDoer, pool *RemotePluginHTTPPool) *HTTPSuggestedPluginProvider {
	return &HTTPSuggestedPluginProvider{
		BaseURL:     strings.TrimSpace(baseURL),
		AccessToken: strings.TrimSpace(accessToken),
		AccountID:   strings.TrimSpace(accountID),
		HTTPClient:  client,
		Pool:        pool,
	}
}

// httpClient resolves the transport for requests: an explicitly supplied doer
// wins, otherwise the owner's shared pool, otherwise the process default.
func (p *HTTPSuggestedPluginProvider) httpClient() HTTPDoer {
	if p == nil {
		return http.DefaultClient
	}
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	if p.Pool != nil {
		return p.Pool.Client()
	}
	return http.DefaultClient
}

// productSKU resolves the request's product SKU, falling back to the bundled
// CODEX_PRODUCT_SKU (Rust #49100).
func (p *HTTPSuggestedPluginProvider) productSKU() string {
	if p != nil {
		if sku := strings.TrimSpace(p.ProductSKU); sku != "" {
			return sku
		}
	}
	return CODEXProductSKU
}

func (p *HTTPSuggestedPluginProvider) ListSuggestedPlugins(ctx context.Context) (*SuggestedPluginList, error) {
	if p == nil {
		return nil, fmt.Errorf("suggested plugin provider is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, suggestedPluginsTimeout)
	defer cancel()
	body, err := p.getSuggestedResource(ctx, suggestedPluginsURL(p.BaseURL))
	if err != nil {
		return nil, err
	}
	return decodeSuggestedPluginResponse(body)
}

// SuggestedPluginServiceScope reports the endpoint and account this provider
// reads recommendations for (Rust #62610 plugin_service_scope).
func (p *HTTPSuggestedPluginProvider) SuggestedPluginServiceScope() SuggestedPluginServiceScope {
	if p == nil {
		return SuggestedPluginServiceScope{}
	}
	return SuggestedPluginServiceScope{
		ChatGPTBaseURL: strings.TrimRight(strings.TrimSpace(p.BaseURL), "/"),
		AccountID:      strings.TrimSpace(p.AccountID),
	}
}

func (p *HTTPSuggestedPluginProvider) getSuggestedResource(ctx context.Context, rawURL string) ([]byte, error) {
	token := strings.TrimSpace(p.AccessToken)
	if token == "" {
		return nil, fmt.Errorf("ChatGPT auth is required to load recommended plugins")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	if accountID := strings.TrimSpace(p.AccountID); accountID != "" {
		request.Header.Set("ChatGPT-Account-ID", accountID)
	}
	request.Header.Set("OAI-Product-Sku", p.productSKU())
	if err := ApplyRuntimeAuth(ctx, request, p.RuntimeAuth); err != nil {
		return nil, err
	}
	response, err := p.httpClient().Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("recommended plugins request failed with status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (s *PluginService) SetSuggestedPluginProviderWithKey(provider SuggestedPluginProvider, key string) {
	if s == nil {
		return
	}
	key = strings.TrimSpace(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	if provider == nil {
		s.suggestedProvider = nil
		s.suggestedProviderKey = ""
		s.suggestedCache = nil
		s.suggestedCacheValid = false
		s.suggestedCheckedAt = time.Time{}
		s.suggestedGeneration++
		s.suggestedFetch = nil
		return
	}
	if s.suggestedProviderKey != key {
		s.suggestedCache = nil
		s.suggestedCacheValid = false
		s.suggestedCheckedAt = time.Time{}
		s.suggestedGeneration++
		s.suggestedFetch = nil
	}
	s.suggestedProvider = provider
	s.suggestedProviderKey = key
}

func (s *PluginService) ClearRecommendedPluginsCache() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.suggestedCache = nil
	s.suggestedCacheValid = false
	s.suggestedCheckedAt = time.Time{}
	s.suggestedGeneration++
	s.suggestedFetch = nil
	s.mu.Unlock()
}

// CurrentRecommendationScope reports the plugin service scope the configured
// provider reads recommendations for.
func (s *PluginService) CurrentRecommendationScope() (SuggestedPluginServiceScope, bool) {
	if s == nil {
		return SuggestedPluginServiceScope{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentRecommendationScopeLocked()
}

func (s *PluginService) currentRecommendationScopeLocked() (SuggestedPluginServiceScope, bool) {
	if s.suggestedProvider == nil {
		return SuggestedPluginServiceScope{}, false
	}
	scoped, ok := s.suggestedProvider.(SuggestedPluginScopeProvider)
	if !ok {
		return SuggestedPluginServiceScope{}, false
	}
	scope := scoped.SuggestedPluginServiceScope()
	// Rust's plugin_service_scope normalizes the endpoint and account once, so
	// invalidation compares canonical scopes.
	return SuggestedPluginServiceScope{
		ChatGPTBaseURL: strings.TrimRight(strings.TrimSpace(scope.ChatGPTBaseURL), "/"),
		AccountID:      strings.TrimSpace(scope.AccountID),
	}, true
}

// InvalidateRecommendations marks the cached recommendation catalog stale after
// a known tooling change for the given plugin service endpoint and accounts
// (Rust #62610 invalidate_recommendations_for_actors). Every product SKU for a
// matching scope refreshes on its next lookup; the last successful catalog
// keeps serving until then.
func (s *PluginService) InvalidateRecommendations(chatGPTBaseURL string, accountIDs ...string) {
	if s == nil {
		return
	}
	baseURL := strings.TrimRight(strings.TrimSpace(chatGPTBaseURL), "/")
	accounts := make(map[string]bool, len(accountIDs))
	for _, accountID := range accountIDs {
		if trimmed := strings.TrimSpace(accountID); trimmed != "" {
			accounts[trimmed] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateRecommendationsLocked(func(scope SuggestedPluginServiceScope) bool {
		if scope.ChatGPTBaseURL != baseURL {
			return false
		}
		return len(accounts) == 0 || accounts[scope.AccountID]
	})
}

// invalidateCurrentRecommendations refreshes the configured scope after an
// installed plugin inventory or enabled-state change.
func (s *PluginService) invalidateCurrentRecommendations() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalidateRecommendationsLocked(nil)
}

// invalidateRecommendationsLocked keeps the last successful catalog as a
// fallback, drops fetch validity so the next lookup refetches, and detaches an
// in-flight fetch so its late completion cannot repopulate the cache.
func (s *PluginService) invalidateRecommendationsLocked(match func(SuggestedPluginServiceScope) bool) {
	if s.suggestedProvider == nil {
		return
	}
	if match != nil {
		if scope, ok := s.currentRecommendationScopeLocked(); ok && !match(scope) {
			return
		}
	}
	s.suggestedGeneration++
	s.suggestedCacheValid = false
	s.suggestedFetch = nil
}

func (s *PluginService) suggestedDiscoverableCandidates(ctx context.Context) ([]DiscoverableInfo, bool) {
	if s == nil {
		return nil, false
	}
	suggested, err := s.suggestedPluginList(ctx)
	if err != nil || suggested == nil {
		return nil, false
	}
	if !suggested.Enabled {
		return nil, false
	}
	return discoverableCandidatesFromSuggested(suggested.Plugins), true
}

// PrewarmSuggestedPlugins fetches and publishes the recommendation catalog
// without requiring plugin details. Context construction still filters the
// cached list against installed/disabled plugins.
func (s *PluginService) PrewarmSuggestedPlugins(ctx context.Context) {
	if s == nil {
		return
	}
	_, _ = s.suggestedPluginList(ctx)
}

// suggestedPluginList returns the published recommendation list or joins one
// in-flight fetch (Rust #62610). A catalog stays valid until a known tooling
// change invalidates it, so lookups no longer poll the endpoint. Concurrent
// lookups share one fetch, a failed refresh keeps the last successful catalog
// and retries on the next lookup, and a fetch that completes after an
// invalidation only answers its own waiters.
func (s *PluginService) suggestedPluginList(ctx context.Context) (*SuggestedPluginList, error) {
	if s == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	provider := s.suggestedProvider
	providerKey := s.suggestedProviderKey
	if provider == nil {
		s.mu.Unlock()
		return nil, nil
	}
	if s.suggestedCacheValid && s.suggestedCache != nil {
		cached := cloneSuggestedPluginList(s.suggestedCache)
		s.mu.Unlock()
		return cached, nil
	}
	if fetch := s.suggestedFetch; fetch != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-fetch.done:
			return cloneSuggestedPluginList(fetch.list), fetch.err
		}
	}
	fetch := &suggestedPluginFetch{done: make(chan struct{})}
	// The invalidation keeps the previous catalog as the fallback for a failed
	// refresh (Rust #62610 RecommendationCacheEntry::for_refresh).
	fallback := cloneSuggestedPluginList(s.suggestedCache)
	generation := s.suggestedGeneration
	s.suggestedFetch = fetch
	s.mu.Unlock()

	catalogSpan := startRecommendationSpan(ctx, RecommendationsFetchCatalogSpanName)
	fetched, err := provider.ListSuggestedPlugins(ctx)
	if catalogSpan != nil {
		catalogSpan.End()
	}
	if err == nil && fetched == nil {
		// Rust RecommendedPluginsMode::Legacy is an endpoint that returned no
		// recommendation catalog.
		fetched = &SuggestedPluginList{}
	}
	if err != nil {
		// Retry on the next lookup because the cache stays stale.
		fetched = fallback
		if fetched == nil {
			fetched = &SuggestedPluginList{}
		}
	}
	s.mu.Lock()
	fetch.list = cloneSuggestedPluginList(fetched)
	fetch.err = nil
	if s.suggestedFetch == fetch {
		s.suggestedFetch = nil
	}
	if err == nil && s.suggestedGeneration == generation &&
		s.suggestedProvider == provider &&
		s.suggestedProviderKey == providerKey {
		s.suggestedCache = cloneSuggestedPluginList(fetched)
		s.suggestedCacheValid = true
		s.suggestedCheckedAt = s.now()
	}
	close(fetch.done)
	s.mu.Unlock()
	return cloneSuggestedPluginList(fetched), nil
}

// discoverableCandidatesFromSuggested projects the endpoint's recommendation
// list. Rust #61374 asks the endpoint for uninstalled plugins
// (`includeInstalled=false`), so installed filtering happens server-side and
// the client only removes tools the user disabled.
func discoverableCandidatesFromSuggested(plugins []SuggestedPlugin) []DiscoverableInfo {
	out := make([]DiscoverableInfo, 0, len(plugins))
	for _, plugin := range plugins {
		id := strings.TrimSpace(firstNonEmpty(plugin.ID, pluginID(plugin.Name, RemoteGlobalMarketplaceName)))
		if id == "" {
			continue
		}
		remoteID := strings.TrimSpace(plugin.RemotePluginID)
		name := strings.TrimSpace(firstNonEmpty(plugin.DisplayName, plugin.Name, id))
		out = append(out, DiscoverableInfo{
			ID:              id,
			RemotePluginID:  remoteID,
			Name:            name,
			Description:     "",
			ToolType:        "plugin",
			AppConnectorIDs: append([]string(nil), plugin.AppConnectorIDs...),
		})
	}
	out = dedupeDiscoverableCandidates(out)
	sort.SliceStable(out, func(i int, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func decodeSuggestedPluginResponse(data []byte) (*SuggestedPluginList, error) {
	var raw struct {
		Enabled bool `json:"enabled"`
		Plugins []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if !raw.Enabled {
		return &SuggestedPluginList{}, nil
	}
	plugins := make([]SuggestedPlugin, 0, len(raw.Plugins))
	seen := map[string]bool{}
	for _, plugin := range raw.Plugins {
		remoteID := strings.TrimSpace(plugin.ID)
		name := strings.TrimSpace(plugin.Name)
		if remoteID == "" || name == "" {
			continue
		}
		id := pluginID(name, RemoteGlobalMarketplaceName)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		displayName := strings.TrimSpace(firstNonEmpty(plugin.DisplayName, name))
		plugins = append(plugins, SuggestedPlugin{
			ID:             id,
			RemotePluginID: remoteID,
			Name:           name,
			DisplayName:    displayName,
		})
	}
	sort.SliceStable(plugins, func(i int, j int) bool {
		return plugins[i].ID < plugins[j].ID
	})
	return &SuggestedPluginList{Enabled: true, Plugins: plugins}, nil
}

func suggestedPluginsURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://chatgpt.com/backend-api"
	}
	// Rust #39143: Codex-specific recommendation route with a compact
	// response shape.
	raw := base + "/ps/plugins/suggested/codex"
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw + "?scope=GLOBAL"
	}
	query := parsed.Query()
	query.Set("scope", "GLOBAL")
	// Rust #61374: the endpoint owns installed-plugin filtering.
	query.Set("includeInstalled", "false")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func cloneSuggestedPluginList(value *SuggestedPluginList) *SuggestedPluginList {
	if value == nil {
		return nil
	}
	clone := &SuggestedPluginList{Enabled: value.Enabled, Plugins: make([]SuggestedPlugin, len(value.Plugins))}
	for i := range value.Plugins {
		clone.Plugins[i] = value.Plugins[i]
		clone.Plugins[i].AppConnectorIDs = append([]string(nil), value.Plugins[i].AppConnectorIDs...)
	}
	return clone
}

func suggestedStatusAllowed(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "AVAILABLE", "ENABLED":
		return true
	default:
		return false
	}
}

func suggestedInstallPolicyAllowed(value string) bool {
	switch strings.TrimSpace(value) {
	case "", string(InstallAllowed):
		return true
	default:
		return false
	}
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
