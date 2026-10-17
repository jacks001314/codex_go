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
)

const RemoteGlobalMarketplaceName = "openai-curated-remote"

const suggestedPluginsTimeout = 5 * time.Second

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type SuggestedPluginProvider interface {
	ListSuggestedPlugins(ctx context.Context) (*SuggestedPluginList, error)
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
	token := strings.TrimSpace(p.AccessToken)
	if token == "" {
		return nil, fmt.Errorf("ChatGPT auth is required to load recommended plugins")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, suggestedPluginsTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, suggestedPluginsURL(p.BaseURL), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	if accountID := strings.TrimSpace(p.AccountID); accountID != "" {
		request.Header.Set("ChatGPT-Account-ID", accountID)
	}
	request.Header.Set("OAI-Product-Sku", p.productSKU())
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
	return decodeSuggestedPluginResponse(body)
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
		s.suggestedGeneration++
		s.suggestedFetch = nil
		return
	}
	if s.suggestedProviderKey != key {
		s.suggestedCache = nil
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
	s.suggestedGeneration++
	s.suggestedFetch = nil
	s.mu.Unlock()
}

func (s *PluginService) suggestedDiscoverableCandidates(ctx context.Context, details []PluginDetail) ([]DiscoverableInfo, bool) {
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
	return discoverableCandidatesFromSuggested(suggested.Plugins, details), true
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
// in-flight fetch. A fetch invalidated by ClearRecommendedPluginsCache or a
// provider change returns its result to existing waiters without repopulating
// the cache (Rust #41375).
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
	cached := cloneSuggestedPluginList(s.suggestedCache)
	if cached != nil {
		s.mu.Unlock()
		return cached, nil
	}
	if provider == nil {
		s.mu.Unlock()
		return nil, nil
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
	generation := s.suggestedGeneration
	s.suggestedFetch = fetch
	s.mu.Unlock()

	fetched, err := provider.ListSuggestedPlugins(ctx)
	s.mu.Lock()
	fetch.list = cloneSuggestedPluginList(fetched)
	fetch.err = err
	if s.suggestedFetch == fetch {
		s.suggestedFetch = nil
	}
	if err == nil && fetched != nil &&
		s.suggestedGeneration == generation &&
		s.suggestedProviderKey == providerKey {
		s.suggestedCache = cloneSuggestedPluginList(fetched)
	}
	close(fetch.done)
	s.mu.Unlock()
	return cloneSuggestedPluginList(fetched), err
}

func discoverableCandidatesFromSuggested(plugins []SuggestedPlugin, details []PluginDetail) []DiscoverableInfo {
	installedIDs := map[string]bool{}
	installedRemoteIDs := map[string]bool{}
	for _, detail := range details {
		summary := detail.Summary
		if !summary.Installed {
			continue
		}
		for _, value := range []string{summary.ID, pluginID(summary.Name, summary.MarketplaceName)} {
			if strings.TrimSpace(value) != "" {
				installedIDs[strings.TrimSpace(value)] = true
			}
		}
		if strings.TrimSpace(summary.RemotePluginID) != "" {
			installedRemoteIDs[strings.TrimSpace(summary.RemotePluginID)] = true
		}
	}
	out := make([]DiscoverableInfo, 0, len(plugins))
	for _, plugin := range plugins {
		id := strings.TrimSpace(firstNonEmpty(plugin.ID, pluginID(plugin.Name, RemoteGlobalMarketplaceName)))
		if id == "" || installedIDs[id] {
			continue
		}
		remoteID := strings.TrimSpace(plugin.RemotePluginID)
		if remoteID != "" && installedRemoteIDs[remoteID] {
			continue
		}
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
