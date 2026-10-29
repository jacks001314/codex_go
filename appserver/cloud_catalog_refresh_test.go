package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/plugin"
)

type countingPluginCatalogProvider struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (p *countingPluginCatalogProvider) ListPlugins(context.Context, plugin.PluginListQuery) (*plugin.PluginCatalog, error) {
	p.mu.Lock()
	p.calls++
	err := p.err
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &plugin.PluginCatalog{}, nil
}

func (p *countingPluginCatalogProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newCloudCatalogTestRouter(t *testing.T, provider any) *RuntimeRouter {
	t.Helper()
	return NewRuntimeRouter(RuntimeServices{
		Plugins:               plugin.NewPluginService(),
		PluginCatalogProvider: provider,
		Config:                config.NewConfigService(t.TempDir()),
	})
}

func TestCloudCatalogBackoffEscalatesToCapLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var backoff *cloudCatalogRefreshBackoff
	want := []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second, 240 * time.Second, 300 * time.Second, 300 * time.Second}
	for index, delay := range want {
		backoff = nextCloudCatalogBackoff("scope", backoff, now)
		if backoff.delay != delay {
			t.Fatalf("failure %d delay = %s, want %s", index+1, backoff.delay, delay)
		}
		if !backoff.pending("scope", now) {
			t.Fatalf("failure %d backoff not pending immediately after failure", index+1)
		}
		if backoff.pending("scope", now.Add(delay)) {
			t.Fatalf("failure %d backoff still pending at retry time", index+1)
		}
		now = now.Add(delay)
	}
}

func TestCloudCatalogBackoffScopedToAuthAndResourcesLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	backoff := nextCloudCatalogBackoff("account-a\x001", nil, now)
	if backoff.pending("account-b\x001", now) {
		t.Fatal("backoff applied to another auth scope")
	}
	if backoff.pending("account-a\x002", now) {
		t.Fatal("backoff applied to another resource generation")
	}
	if !backoff.pending("account-a\x001", now.Add(29*time.Second)) {
		t.Fatal("backoff not pending within its window")
	}
}

func TestRefreshCloudPluginCatalogForStepBacksOffFailuresLikeRust(t *testing.T) {
	provider := &countingPluginCatalogProvider{err: errors.New("cloud plugin discovery unavailable")}
	router := newCloudCatalogTestRouter(t, provider)
	now := time.Unix(1_700_000_000, 0)
	router.cloudCatalogs.forThread("thread-a").now = func() time.Time { return now }
	cfg := &config.Config{}

	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-1")
	if got := provider.count(); got != 1 {
		t.Fatalf("first step discovery calls = %d, want 1", got)
	}
	now = now.Add(29 * time.Second)
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-2")
	if got := provider.count(); got != 1 {
		t.Fatalf("step inside cooldown discovery calls = %d, want 1", got)
	}
	now = now.Add(1 * time.Second)
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-3")
	if got := provider.count(); got != 2 {
		t.Fatalf("step after cooldown discovery calls = %d, want 2", got)
	}
	// The second failure doubles the cooldown: 60s from now.
	now = now.Add(59 * time.Second)
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-4")
	if got := provider.count(); got != 2 {
		t.Fatalf("step inside doubled cooldown discovery calls = %d, want 2", got)
	}
	now = now.Add(1 * time.Second)
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-5")
	if got := provider.count(); got != 3 {
		t.Fatalf("step after doubled cooldown discovery calls = %d, want 3", got)
	}
	// Cooldowns are per thread, so another thread discovers immediately.
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-b", "turn-1")
	if got := provider.count(); got != 4 {
		t.Fatalf("other thread discovery calls = %d, want 4", got)
	}
}

func TestRefreshCloudPluginCatalogForStepRetriesCancellationImmediatelyLikeRust(t *testing.T) {
	provider := &countingPluginCatalogProvider{err: fmt.Errorf("refresh aborted: %w", context.Canceled)}
	router := newCloudCatalogTestRouter(t, provider)
	cfg := &config.Config{}

	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-1")
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-2")
	if got := provider.count(); got != 2 {
		t.Fatalf("cancelled discovery calls = %d, want an immediate retry", got)
	}
	if router.cloudCatalogs.forThread("thread-a").pluginBackoffPending(router.cloudPluginCatalogScope()) {
		t.Fatal("cancelled discovery installed a cooldown")
	}
}

func TestRefreshCloudPluginCatalogForStepReusesTurnDiscoveryLikeRust(t *testing.T) {
	provider := &countingPluginCatalogProvider{}
	router := newCloudCatalogTestRouter(t, provider)
	cfg := &config.Config{}

	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-1")
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-1")
	if got := provider.count(); got != 1 {
		t.Fatalf("same-turn discovery calls = %d, want 1", got)
	}
	if key := router.services.Plugins.CloudPluginCatalogKey(); key != router.cloudPluginCatalogScope() {
		t.Fatalf("published catalog key = %q, want %q", key, router.cloudPluginCatalogScope())
	}
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-2")
	if got := provider.count(); got != 2 {
		t.Fatalf("next-turn discovery calls = %d, want 2", got)
	}
}

func TestRefreshCloudPluginCatalogForStepSerializesConcurrentStepsLikeRust(t *testing.T) {
	provider := &countingPluginCatalogProvider{}
	router := newCloudCatalogTestRouter(t, provider)
	cfg := &config.Config{}
	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-a", "turn-1")
		}()
	}
	wait.Wait()
	if got := provider.count(); got != 1 {
		t.Fatalf("concurrent same-turn discovery calls = %d, want 1", got)
	}
}

func TestOrchestratorSkillDiscoveryBacksOffAndWarnsOncePerTurnLikeRust(t *testing.T) {
	var requestMu sync.Mutex
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode MCP request error = %v", err)
			return
		}
		requestMu.Lock()
		requestCount++
		requestMu.Unlock()
		writeRuntimeRouterMCPError(t, w, request.ID, -32000, "temporary orchestrator failure", nil)
	}))
	defer server.Close()
	router := NewRuntimeRouter(RuntimeServices{
		MCP: mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
			mcp.RuntimeCodexAppsMCPServerName: {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
		}}),
	})
	threadID := "thread-cloud-skill-backoff"
	now := time.Unix(1_700_000_000, 0)
	router.cloudCatalogs.forThread(threadID).now = func() time.Time { return now }

	_, firstWarnings := router.orchestratorSkillMetadataForRuntime(threadID, "turn-1")
	requestMu.Lock()
	firstRequestCount := requestCount
	requestMu.Unlock()
	_, repeatWarnings := router.orchestratorSkillMetadataForRuntime(threadID, "turn-1")
	if len(firstWarnings) != 1 || !strings.Contains(firstWarnings[0], "orchestrator skills unavailable:") {
		t.Fatalf("first warnings = %#v", firstWarnings)
	}
	if len(repeatWarnings) != 0 {
		t.Fatalf("same-turn warnings = %#v, want once per turn", repeatWarnings)
	}
	if firstRequestCount == 0 {
		t.Fatal("cloud skill discovery never ran")
	}
	service := router.mcpServiceForThread(threadID, nil)
	if !router.cloudCatalogs.forThread(threadID).skillBackoffPending(router.cloudSkillCatalogScope(service)) {
		t.Fatal("failed cloud skill discovery did not install a cooldown")
	}
	// A later turn inside the cooldown must not start discovery or report the
	// failure again: Rust returns before discovering while a retry is pending.
	now = now.Add(20 * time.Second)
	_, cooledWarnings := router.orchestratorSkillMetadataForRuntime(threadID, "turn-2")
	requestMu.Lock()
	cooledRequestCount := requestCount
	requestMu.Unlock()
	if len(cooledWarnings) != 0 {
		t.Fatalf("cooldown warnings = %#v, want none while a retry is pending", cooledWarnings)
	}
	if cooledRequestCount > firstRequestCount {
		t.Fatalf("cooldown discovery requests = %d, want no new requests after %d", cooledRequestCount, firstRequestCount)
	}
	// Once the cooldown elapses, a new resource generation retries discovery.
	now = now.Add(11 * time.Second)
	router.services.MCP.Refresh()
	_, retriedWarnings := router.orchestratorSkillMetadataForRuntime(threadID, "turn-3")
	requestMu.Lock()
	retriedRequestCount := requestCount
	requestMu.Unlock()
	if retriedRequestCount <= cooledRequestCount {
		t.Fatalf("retry requests = %d, want a discovery after the cooldown", retriedRequestCount)
	}
	if len(retriedWarnings) != 1 {
		t.Fatalf("retry warnings = %#v, want the failure reported again in a new turn", retriedWarnings)
	}
}
