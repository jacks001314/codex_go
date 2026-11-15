package plugin

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type toolingAwareSuggestedProvider struct {
	mu         sync.Mutex
	fetches    int
	markerPoll int
	marker     string
	markerOK   bool
	markerErr  error
	listErr    error
	plugins    []SuggestedPlugin
}

func (p *toolingAwareSuggestedProvider) ListSuggestedPlugins(context.Context) (*SuggestedPluginList, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetches++
	if p.listErr != nil {
		return nil, p.listErr
	}
	return &SuggestedPluginList{Enabled: true, Plugins: append([]SuggestedPlugin(nil), p.plugins...)}, nil
}

func (p *toolingAwareSuggestedProvider) SuggestedPluginsChangedAt(context.Context) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.markerPoll++
	return p.marker, p.markerOK, p.markerErr
}

func (p *toolingAwareSuggestedProvider) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetches, p.markerPoll
}

// TestSuggestedPluginsRefreshOnToolingStateChangeLikeRust mirrors Rust #61387:
// a catalog younger than the reuse window is served without probing, an
// unchanged marker keeps it, and a changed marker refreshes it.
func TestSuggestedPluginsRefreshOnToolingStateChangeLikeRust(t *testing.T) {
	service := NewPluginService()
	now := time.Unix(1_700_000_000, 0)
	service.SetClock(func() time.Time { return now })
	provider := &toolingAwareSuggestedProvider{
		marker:   "changed-at-1",
		markerOK: true,
		plugins:  []SuggestedPlugin{{ID: "one@openai-curated-remote", Name: "one"}},
	}
	service.SetSuggestedPluginProviderWithKey(provider, "endpoint")

	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("initial fetch error = %v", err)
	}
	if fetches, polls := provider.counts(); fetches != 1 || polls != 1 {
		t.Fatalf("initial counts = %d/%d, want 1/1", fetches, polls)
	}

	now = now.Add(500 * time.Millisecond)
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("reuse-window fetch error = %v", err)
	}
	if fetches, polls := provider.counts(); fetches != 1 || polls != 1 {
		t.Fatalf("reuse-window counts = %d/%d, want no new work", fetches, polls)
	}

	now = now.Add(2 * time.Second)
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("unchanged-marker fetch error = %v", err)
	}
	if fetches, polls := provider.counts(); fetches != 1 || polls != 2 {
		t.Fatalf("unchanged-marker counts = %d/%d, want 1/2", fetches, polls)
	}

	provider.mu.Lock()
	provider.marker = "changed-at-2"
	provider.plugins = []SuggestedPlugin{{ID: "two@openai-curated-remote", Name: "two"}}
	provider.mu.Unlock()
	now = now.Add(2 * time.Second)
	list, err := service.suggestedPluginList(context.Background())
	if err != nil {
		t.Fatalf("changed-marker fetch error = %v", err)
	}
	if fetches, polls := provider.counts(); fetches != 2 || polls != 3 {
		t.Fatalf("changed-marker counts = %d/%d, want 2/3", fetches, polls)
	}
	if len(list.Plugins) != 1 || list.Plugins[0].ID != "two@openai-curated-remote" {
		t.Fatalf("refreshed catalog = %#v", list)
	}
}

// TestSuggestedPluginsKeepCatalogWhenToolingStateUnavailableLikeRust mirrors
// Rust #61387: an absent or failing marker keeps the published catalog, and a
// failed refresh retains it as well.
func TestSuggestedPluginsKeepCatalogWhenToolingStateUnavailableLikeRust(t *testing.T) {
	service := NewPluginService()
	now := time.Unix(1_700_000_000, 0)
	service.SetClock(func() time.Time { return now })
	provider := &toolingAwareSuggestedProvider{
		plugins: []SuggestedPlugin{{ID: "one@openai-curated-remote", Name: "one"}},
	}
	service.SetSuggestedPluginProviderWithKey(provider, "endpoint")
	if _, err := service.suggestedPluginList(context.Background()); err != nil {
		t.Fatalf("initial fetch error = %v", err)
	}

	now = now.Add(2 * time.Second)
	if list, err := service.suggestedPluginList(context.Background()); err != nil || list == nil || len(list.Plugins) != 1 {
		t.Fatalf("unknown marker result = %#v, %v", list, err)
	}
	if fetches, _ := provider.counts(); fetches != 1 {
		t.Fatalf("fetches = %d, want the catalog reused without a marker", fetches)
	}

	provider.mu.Lock()
	provider.marker, provider.markerOK = "changed-at-2", true
	provider.markerErr = errors.New("tooling state unavailable")
	provider.mu.Unlock()
	now = now.Add(2 * time.Second)
	if list, err := service.suggestedPluginList(context.Background()); err != nil || list == nil || len(list.Plugins) != 1 {
		t.Fatalf("marker error result = %#v, %v", list, err)
	}

	provider.mu.Lock()
	provider.markerErr = nil
	provider.listErr = errors.New("catalog unavailable")
	provider.mu.Unlock()
	now = now.Add(2 * time.Second)
	if list, err := service.suggestedPluginList(context.Background()); err != nil || list == nil || len(list.Plugins) != 1 {
		t.Fatalf("failed refresh result = %#v, %v", list, err)
	}
}

func TestSuggestedToolingStateURLLikeRust(t *testing.T) {
	if got := suggestedToolingStateURL("https://chatgpt.com/backend-api/"); got != "https://chatgpt.com/backend-api/ps/plugins/tooling-state/turtle" {
		t.Fatalf("tooling state URL = %q", got)
	}
}
