package plugin

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDecodeSuggestedPluginResponseCompactShapeLikeRust(t *testing.T) {
	// Rust #39143: the Codex-specific /ps/plugins/suggested/codex endpoint
	// returns a compact response without per-plugin status/policy/release.
	data := []byte(`{
		"enabled": true,
		"plugins": [
			{"id": "remote-1", "name": "docs", "display_name": "Docs Connector"},
			{"id": "remote-2", "name": "calendar", "display_name": "Calendar"}
		]
	}`)
	list, err := decodeSuggestedPluginResponse(data)
	if err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if !list.Enabled || len(list.Plugins) != 2 {
		t.Fatalf("list = %#v", list)
	}
	byName := map[string]SuggestedPlugin{}
	for _, plugin := range list.Plugins {
		byName[plugin.Name] = plugin
	}
	docs, ok := byName["docs"]
	if !ok || docs.RemotePluginID != "remote-1" || docs.DisplayName != "Docs Connector" {
		t.Fatalf("docs plugin = %#v ok=%v", docs, ok)
	}
	if !strings.HasPrefix(docs.ID, "docs@") {
		t.Fatalf("docs plugin id = %q", docs.ID)
	}
}

func TestDecodeSuggestedPluginResponseDisabledLikeRust(t *testing.T) {
	list, err := decodeSuggestedPluginResponse([]byte(`{"enabled": false, "plugins": []}`))
	if err != nil {
		t.Fatalf("decode error = %v", err)
	}
	if list.Enabled || len(list.Plugins) != 0 {
		t.Fatalf("disabled list = %#v", list)
	}
}

func TestSuggestedPluginsURLCodexRouteLikeRust(t *testing.T) {
	url := suggestedPluginsURL("https://chatgpt.com/backend-api")
	if !strings.Contains(url, "/ps/plugins/suggested/codex") || !strings.Contains(url, "scope=GLOBAL") {
		t.Fatalf("suggested plugins URL = %q", url)
	}
}

type gatedSuggestedPluginProvider struct {
	calls chan *gatedSuggestedPluginCall
}

type gatedSuggestedPluginCall struct {
	release chan struct{}
	list    *SuggestedPluginList
	err     error
}

func (p *gatedSuggestedPluginProvider) ListSuggestedPlugins(ctx context.Context) (*SuggestedPluginList, error) {
	call := &gatedSuggestedPluginCall{release: make(chan struct{})}
	select {
	case p.calls <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-call.release:
		return call.list, call.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSuggestedPluginsInvalidatedFetchDoesNotRepopulateCacheLikeRust(t *testing.T) {
	provider := &gatedSuggestedPluginProvider{calls: make(chan *gatedSuggestedPluginCall)}
	service := NewPluginService()
	service.SetSuggestedPluginProviderWithKey(provider, "endpoint")

	type result struct {
		list *SuggestedPluginList
		err  error
	}
	first := make(chan result, 1)
	go func() {
		list, err := service.suggestedPluginList(context.Background())
		first <- result{list: list, err: err}
	}()
	oldCall := <-provider.calls
	service.ClearRecommendedPluginsCache()

	second := make(chan result, 1)
	go func() {
		list, err := service.suggestedPluginList(context.Background())
		second <- result{list: list, err: err}
	}()
	newCall := <-provider.calls
	newCall.list = &SuggestedPluginList{Enabled: true}
	close(newCall.release)
	newResult := <-second
	if newResult.err != nil || newResult.list == nil || !newResult.list.Enabled || len(newResult.list.Plugins) != 0 {
		t.Fatalf("new fetch = %#v, %v", newResult.list, newResult.err)
	}

	oldCall.list = &SuggestedPluginList{Enabled: true, Plugins: []SuggestedPlugin{{ID: "old@market", Name: "Old"}}}
	close(oldCall.release)
	oldResult := <-first
	if oldResult.err != nil || oldResult.list == nil || len(oldResult.list.Plugins) != 1 || oldResult.list.Plugins[0].ID != "old@market" {
		t.Fatalf("old fetch = %#v, %v", oldResult.list, oldResult.err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	cached, err := service.suggestedPluginList(ctx)
	if err != nil || cached == nil || len(cached.Plugins) != 0 {
		t.Fatalf("cache after stale completion = %#v, %v", cached, err)
	}
	select {
	case call := <-provider.calls:
		t.Fatalf("unexpected extra fetch: %#v", call)
	default:
	}
}

func TestSuggestedPluginsSharesInFlightFetchAndPrewarmsCacheLikeRust(t *testing.T) {
	provider := &gatedSuggestedPluginProvider{calls: make(chan *gatedSuggestedPluginCall)}
	service := NewPluginService()
	service.SetSuggestedPluginProviderWithKey(provider, "endpoint")

	results := make(chan *SuggestedPluginList, 2)
	for range 2 {
		go func() {
			list, _ := service.suggestedPluginList(context.Background())
			results <- list
		}()
	}
	call := <-provider.calls
	call.list = &SuggestedPluginList{Enabled: true, Plugins: []SuggestedPlugin{{ID: "shared@market", Name: "Shared"}}}
	close(call.release)
	for range 2 {
		list := <-results
		if list == nil || len(list.Plugins) != 1 || list.Plugins[0].ID != "shared@market" {
			t.Fatalf("shared result = %#v", list)
		}
	}
	service.PrewarmSuggestedPlugins(context.Background())
	select {
	case extra := <-provider.calls:
		t.Fatalf("prewarm refetched a cached catalog: %#v", extra)
	default:
	}
}

func TestSuggestedPluginsPrewarmPopulatesCacheLikeRust(t *testing.T) {
	provider := &gatedSuggestedPluginProvider{calls: make(chan *gatedSuggestedPluginCall)}
	service := NewPluginService()
	service.SetSuggestedPluginProviderWithKey(provider, "endpoint")

	done := make(chan struct{})
	go func() {
		service.PrewarmSuggestedPlugins(context.Background())
		close(done)
	}()
	call := <-provider.calls
	call.list = &SuggestedPluginList{Enabled: true, Plugins: []SuggestedPlugin{{ID: "prewarmed@market", Name: "Prewarmed"}}}
	close(call.release)
	<-done

	cached, err := service.suggestedPluginList(context.Background())
	if err != nil || cached == nil || len(cached.Plugins) != 1 || cached.Plugins[0].ID != "prewarmed@market" {
		t.Fatalf("prewarmed cache = %#v, %v", cached, err)
	}
	select {
	case extra := <-provider.calls:
		t.Fatalf("cached prewarm refetched: %#v", extra)
	default:
	}
}
