package model

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func originatorTestCatalog(description string, visibility string) ModelsResponse {
	return ModelsResponse{Models: []ModelInfo{{
		Slug:           "gpt-5.5",
		DisplayName:    description,
		Description:    description,
		Visibility:     visibility,
		SupportedInAPI: true,
	}}}
}

// Mirrors Rust #56284: supported callers send their own originator header and
// keep isolated in-memory catalogs and disk caches; unsupported callers retain
// the default manager.
func TestModelCatalogOriginatorsPartitionHeadersAndCachesLikeRust(t *testing.T) {
	catalogs := map[string]ModelsResponse{
		"provider-default":   originatorTestCatalog("provider-default", VisibilityList),
		"codex_work_cca":     originatorTestCatalog("codex_work_cca", VisibilityList),
		"codex_work_web":     originatorTestCatalog("codex_work_web", VisibilityList),
		"codex_work_desktop": originatorTestCatalog("codex_work_desktop", VisibilityList),
		"codex_work_mobile":  originatorTestCatalog("codex_work_mobile", VisibilityList),
		"chatgpt_cca":        originatorTestCatalog("chatgpt_cca", VisibilityList),
	}
	var mu sync.Mutex
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originator := r.Header.Get("originator")
		mu.Lock()
		calls[originator]++
		mu.Unlock()
		catalog, ok := catalogs[originator]
		if !ok {
			http.Error(w, "unexpected originator", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(catalog)
	}))
	defer server.Close()

	home := t.TempDir()
	newManager := func() *RemoteModelsManager {
		endpoint := NewHTTPModelsEndpoint(&APIProvider{
			Name:    OpenAIProviderName,
			BaseURL: server.URL,
			Headers: http.Header{"Originator": []string{"provider-default"}},
		}, nil, server.Client())
		manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
			Endpoint:                endpoint,
			Identity:                "identity-1",
			ExplicitProviderCatalog: true,
		})
		manager.ConfigureCache(home)
		return manager
	}

	manager := newManager()
	work, ok := ModelsManagerForOriginatorWithOK(manager, "codex_work_cca")
	if !ok {
		t.Fatal("codex_work_cca did not select an originator manager")
	}
	chat, ok := ModelsManagerForOriginatorWithOK(manager, "chatgpt_cca")
	if !ok {
		t.Fatal("chatgpt_cca did not select an originator manager")
	}
	if work == chat {
		t.Fatal("different originators shared a manager")
	}
	again, ok := ModelsManagerForOriginatorWithOK(manager, "codex_work_cca")
	if !ok || again != work {
		t.Fatal("repeated originator did not reuse its manager")
	}
	for _, unsupported := range []string{"", "unknown", "codex_cli_rs", "codex_vscode", "chatgpt_cca_local_dev"} {
		if child, ok := ModelsManagerForOriginatorWithOK(manager, unsupported); ok || child != nil {
			t.Fatalf("unsupported originator %q selected manager %T", unsupported, child)
		}
	}

	originators := []string{"codex_work_cca", "codex_work_web", "codex_work_desktop", "codex_work_mobile", "chatgpt_cca"}
	type catalogResult struct {
		name string
		desc string
		err  string
	}
	results := make(chan catalogResult, len(originators)+1)
	var wg sync.WaitGroup
	collect := func(name string, models ManagerPartition) {
		defer wg.Done()
		catalog := models.RawModelCatalog(RefreshOnline)
		if len(catalog.Models) != 1 {
			results <- catalogResult{name: name, err: "unexpected model count"}
			return
		}
		results <- catalogResult{name: name, desc: catalog.Models[0].Description}
	}
	wg.Add(1)
	go collect("", manager)
	for _, originator := range originators {
		scoped, ok := ModelsManagerForOriginatorWithOK(manager, originator)
		if !ok {
			t.Fatalf("%s did not select an originator manager", originator)
		}
		wg.Add(1)
		go collect(originator, scoped)
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result.err != "" {
			t.Fatalf("%s: %s", result.name, result.err)
		}
		want := result.name
		if want == "" {
			want = "provider-default"
		}
		if result.desc != want {
			t.Fatalf("%s catalog description = %q, want %q", result.name, result.desc, want)
		}
	}
	mu.Lock()
	for _, originator := range append([]string{"provider-default"}, originators...) {
		if calls[originator] != 1 {
			mu.Unlock()
			t.Fatalf("originator %q requests = %d, want 1", originator, calls[originator])
		}
	}
	mu.Unlock()

	for _, name := range []string{"models_cache.json", "models_cache.codex_work_cca.json", "models_cache.chatgpt_cca.json"} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Fatalf("cache %s: %v", name, err)
		}
	}

	restarted := newManager()
	restartedWork, ok := ModelsManagerForOriginatorWithOK(restarted, "codex_work_cca")
	if !ok {
		t.Fatal("restarted manager did not select codex_work_cca")
	}
	_ = restarted.RawModelCatalog(RefreshOnlineIfUncached)
	workCatalog := restartedWork.RawModelCatalog(RefreshOnlineIfUncached)
	if len(workCatalog.Models) != 1 || workCatalog.Models[0].Description != "codex_work_cca" {
		t.Fatalf("restarted work catalog = %#v", workCatalog.Models)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, originator := range []string{"provider-default", "codex_work_cca"} {
		if calls[originator] != 1 {
			t.Fatalf("restart refetched originator %q (%d requests)", originator, calls[originator])
		}
	}
}

// ManagerPartition is the subset used by the concurrency test.
type ManagerPartition interface {
	RawModelCatalog(RefreshStrategy) ModelsResponse
}

// Mirrors Rust #56284: catalog discovery policy is shared by existing and newly
// created originator partitions.
func TestOriginatorManagersShareAPIKeyDiscoveryPolicyLikeRust(t *testing.T) {
	catalog := originatorTestCatalog("remote", VisibilityList)
	var mu sync.Mutex
	calls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originator := r.Header.Get("originator")
		mu.Lock()
		calls[originator]++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(catalog)
	}))
	defer server.Close()

	manager := NewRemoteModelsManagerWithOptions(&RemoteModelsManagerOptions{
		ModelCatalog:         &ModelsResponse{Models: []ModelInfo{{Slug: "bundled", Visibility: VisibilityVisible, SupportedInAPI: true}}},
		Endpoint:             NewHTTPModelsEndpoint(&APIProvider{BaseURL: server.URL}, nil, server.Client()),
		Identity:             "identity-1",
		SupportsAPIKeyModels: true,
		APIKeyAuth:           true,
	})
	work, ok := ModelsManagerForOriginatorWithOK(manager, "codex_work_cca")
	if !ok {
		t.Fatal("codex_work_cca did not select an originator manager")
	}
	chat, ok := ModelsManagerForOriginatorWithOK(manager, "chatgpt_cca")
	if !ok {
		t.Fatal("chatgpt_cca did not select an originator manager")
	}
	if got := work.RawModelCatalog(RefreshOnline).Models[0].Slug; got != "bundled" {
		t.Fatalf("disabled discovery catalog = %q, want bundled", got)
	}

	manager.SetAPIKeyModelDiscoveryEnabled(true)
	for _, child := range []ModelsManager{work, chat} {
		got := child.RawModelCatalog(RefreshOnlineIfUncached)
		if len(got.Models) != 1 || got.Models[0].Slug != "gpt-5.5" {
			t.Fatalf("enabled discovery catalog = %#v", got.Models)
		}
	}
	manager.SetAPIKeyModelDiscoveryEnabled(false)
	for _, child := range []ModelsManager{work, chat} {
		got := child.GetRemoteModels()
		if len(got) != 1 || got[0].Slug != "bundled" {
			t.Fatalf("disabled discovery models = %#v, want bundled", got)
		}
	}
	manager.SetAPIKeyModelDiscoveryEnabled(true)
	for _, child := range []ModelsManager{work, chat} {
		got := child.RawModelCatalog(RefreshOnlineIfUncached)
		if len(got.Models) != 1 || got.Models[0].Slug != "gpt-5.5" {
			t.Fatalf("re-enabled discovery catalog = %#v", got.Models)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, originator := range []string{"codex_work_cca", "chatgpt_cca"} {
		if calls[originator] != 1 {
			t.Fatalf("originator %q requests = %d, want 1", originator, calls[originator])
		}
	}
}
