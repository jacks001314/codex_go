package plugin

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// scopedSuggestedProvider counts catalog reads and reports the plugin service
// scope the recommendations belong to (Rust #62610 PluginServiceScope).
type scopedSuggestedProvider struct {
	mu        sync.Mutex
	fetches   int
	list      *SuggestedPluginList
	err       error
	baseURL   string
	accountID string
}

func (p *scopedSuggestedProvider) ListSuggestedPlugins(context.Context) (*SuggestedPluginList, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetches++
	if p.err != nil {
		return nil, p.err
	}
	if p.list == nil {
		return &SuggestedPluginList{Enabled: true}, nil
	}
	return cloneSuggestedPluginList(p.list), nil
}

func (p *scopedSuggestedProvider) SuggestedPluginServiceScope() SuggestedPluginServiceScope {
	return SuggestedPluginServiceScope{ChatGPTBaseURL: p.baseURL, AccountID: p.accountID}
}

func (p *scopedSuggestedProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetches
}

func (p *scopedSuggestedProvider) set(list *SuggestedPluginList, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.list = list
	p.err = err
}

func newScopedSuggestedService(t *testing.T, provider *scopedSuggestedProvider) *PluginService {
	t.Helper()
	service := NewPluginService()
	now := time.Unix(1_700_000_000, 0)
	service.SetClock(func() time.Time { return now })
	service.SetSuggestedPluginProviderWithKey(provider, "endpoint")
	return service
}

// Mirrors Rust #62610: lookups reuse the published catalog instead of polling
// the plugin service for a tooling marker.
func TestSuggestedPluginsReuseCatalogWithoutPollingLikeRust(t *testing.T) {
	provider := &scopedSuggestedProvider{
		baseURL:   "https://chatgpt.com/backend-api",
		accountID: "account-1",
		list:      &SuggestedPluginList{Enabled: true, Plugins: []SuggestedPlugin{{ID: "one@openai-curated-remote", Name: "one"}}},
	}
	service := newScopedSuggestedService(t, provider)
	for range 3 {
		list, err := service.suggestedPluginList(context.Background())
		if err != nil || list == nil || len(list.Plugins) != 1 {
			t.Fatalf("suggestedPluginList() = %#v, %v", list, err)
		}
	}
	if got := provider.count(); got != 1 {
		t.Fatalf("catalog fetches = %d, want the catalog reused", got)
	}
}

// Mirrors Rust #62610: a known tooling change invalidates every product SKU for
// the affected plugin service and account scope, and only that scope.
func TestSuggestedPluginsInvalidateMatchingScopeLikeRust(t *testing.T) {
	provider := &scopedSuggestedProvider{
		baseURL:   "https://chatgpt.com/backend-api/",
		accountID: "account-1",
		list:      &SuggestedPluginList{Enabled: true, Plugins: []SuggestedPlugin{{ID: "one@openai-curated-remote", Name: "one"}}},
	}
	service := newScopedSuggestedService(t, provider)
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("initial fetch error = %v", err)
	}
	provider.set(&SuggestedPluginList{Enabled: true, Plugins: []SuggestedPlugin{{ID: "two@openai-curated-remote", Name: "two"}}}, nil)

	service.InvalidateRecommendations("https://chatgpt.com/backend-api", "other-account")
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("other-account lookup error = %v", err)
	}
	if got := provider.count(); got != 1 {
		t.Fatalf("catalog fetches = %d, want another account to keep its catalog", got)
	}

	service.InvalidateRecommendations("https://chatgpt.com/backend-api", "account-1")
	list, err := service.suggestedPluginList(context.Background())
	if err != nil {
		t.Fatalf("invalidated lookup error = %v", err)
	}
	if got := provider.count(); got != 2 {
		t.Fatalf("catalog fetches = %d, want a refresh after invalidation", got)
	}
	if list == nil || len(list.Plugins) != 1 || list.Plugins[0].ID != "two@openai-curated-remote" {
		t.Fatalf("refreshed catalog = %#v", list)
	}

	service.InvalidateRecommendations("https://other.example.test", "account-1")
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("other-endpoint lookup error = %v", err)
	}
	if got := provider.count(); got != 2 {
		t.Fatalf("catalog fetches = %d, want another backend to keep its catalog", got)
	}
}

// Mirrors Rust #62610: a failed refresh keeps serving the last successful
// catalog and retries on the next lookup.
func TestSuggestedPluginsKeepCatalogWhenRefreshFailsLikeRust(t *testing.T) {
	provider := &scopedSuggestedProvider{
		baseURL:   "https://chatgpt.com/backend-api",
		accountID: "account-1",
		list:      &SuggestedPluginList{Enabled: true, Plugins: []SuggestedPlugin{{ID: "one@openai-curated-remote", Name: "one"}}},
	}
	service := newScopedSuggestedService(t, provider)
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("initial fetch error = %v", err)
	}
	provider.set(nil, errors.New("catalog unavailable"))
	service.InvalidateRecommendations("https://chatgpt.com/backend-api", "account-1")
	list, err := service.suggestedPluginList(context.Background())
	if err != nil || list == nil || len(list.Plugins) != 1 || list.Plugins[0].ID != "one@openai-curated-remote" {
		t.Fatalf("fallback catalog = %#v, %v", list, err)
	}
	if got := provider.count(); got != 2 {
		t.Fatalf("catalog fetches = %d, want the failed refresh attempted", got)
	}
	provider.set(&SuggestedPluginList{Enabled: true, Plugins: []SuggestedPlugin{{ID: "two@openai-curated-remote", Name: "two"}}}, nil)
	list, err = service.suggestedPluginList(context.Background())
	if err != nil || list == nil || len(list.Plugins) != 1 || list.Plugins[0].ID != "two@openai-curated-remote" {
		t.Fatalf("retried catalog = %#v, %v", list, err)
	}
	if got := provider.count(); got != 3 {
		t.Fatalf("catalog fetches = %d, want a retry on the next lookup", got)
	}
}

// Mirrors Rust #62610: publishing a changed installed inventory invalidates the
// recommendation catalog, while republishing the same state does not.
func TestInstalledInventoryChangeInvalidatesRecommendationsLikeRust(t *testing.T) {
	provider := &scopedSuggestedProvider{
		baseURL:   "https://chatgpt.com/backend-api",
		accountID: "account-1",
	}
	service := newScopedSuggestedService(t, provider)
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("initial fetch error = %v", err)
	}
	installed := PluginDetail{Summary: PluginSummary{
		ID:              "linear@openai-curated-remote",
		Name:            "linear",
		DisplayName:     "Linear",
		MarketplaceName: RemoteGlobalMarketplaceName,
		RemotePluginID:  "plugins~Plugin_linear",
		Installed:       true,
		Enabled:         true,
	}}
	if changed := service.ReplaceInstalledRemotePlugins(RemoteGlobalMarketplaceName, []PluginDetail{installed}); !changed {
		t.Fatal("installing a remote plugin reported no change")
	}
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("post-install lookup error = %v", err)
	}
	if got := provider.count(); got != 2 {
		t.Fatalf("catalog fetches = %d, want a refresh after the inventory change", got)
	}
	if changed := service.ReplaceInstalledRemotePlugins(RemoteGlobalMarketplaceName, []PluginDetail{installed}); changed {
		t.Fatal("republishing the same inventory reported a change")
	}
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("unchanged-inventory lookup error = %v", err)
	}
	if got := provider.count(); got != 2 {
		t.Fatalf("catalog fetches = %d, want the unchanged snapshot to keep the catalog", got)
	}
}

func TestSuggestedPluginServiceScopeTrimsBaseURLLikeRust(t *testing.T) {
	provider := NewHTTPSuggestedPluginProvider("https://chatgpt.com/backend-api/", "token", "account-1", nil)
	scope := provider.SuggestedPluginServiceScope()
	if scope.ChatGPTBaseURL != "https://chatgpt.com/backend-api" {
		t.Fatalf("scope base URL = %q", scope.ChatGPTBaseURL)
	}
	if !strings.EqualFold(scope.AccountID, "account-1") {
		t.Fatalf("scope account = %q", scope.AccountID)
	}
}

var _ SuggestedPluginScopeProvider = (*HTTPSuggestedPluginProvider)(nil)
