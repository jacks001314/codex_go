package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newCatalogCacheService builds a chat-scoped service for the process-scoped
// catalog cache tests. `startup_readiness = "catalog"` is what lets a warm
// catalog satisfy startup; without it the server always starts eagerly.
func newCatalogCacheService(t *testing.T, server string, executable string, logPath string, mode string, clock func() time.Time) *MCPService {
	t.Helper()
	return newCatalogCacheServiceWithProtocol(t, server, executable, logPath, mode, MCPProtocolLegacy, clock)
}

// newCatalogCacheServiceWithProtocol pins the connection protocol so a test can
// compare a legacy chat with one that probes `server/discover`.
func newCatalogCacheServiceWithProtocol(t *testing.T, server string, executable string, logPath string, mode string, protocol MCPProtocolMode, clock func() time.Time) *MCPService {
	t.Helper()
	env := map[string]string{
		mcpSharingHelperEnv:     "1",
		mcpSharingHelperModeEnv: mode,
		mcpSharingHelperLogEnv:  logPath,
	}
	config := ServerConfig{
		Enabled:          true,
		Command:          executable,
		Args:             []string{"-test.run=^TestMCPConnectionSharingHelperProcess$", "--"},
		StartupTimeout:   10 * time.Second,
		StartupReadiness: MCPStartupReadinessCatalog,
		Env:              env,
	}
	if protocol != MCPProtocolLegacy {
		override := protocol
		config.ProtocolModeOverride = &override
		config.ProtocolMode = protocol
	}
	service := NewMCPService(&RuntimeConfig{
		Servers: map[string]ServerRegistration{
			server: {
				Name:   server,
				Config: config,
			},
		},
	})
	// Tests that assert re-starts must not join a pooled connection, and the
	// clock has to be controllable for the TTL case.
	service.discoverLocalStdio = false
	service.toolCatalogClock = clock
	t.Cleanup(func() {
		_ = service.Close()
		dropToolCatalogCacheEntry(t, server, service)
	})
	return service
}

func dropToolCatalogCacheEntry(t *testing.T, server string, service *MCPService) {
	t.Helper()
	config, ok := service.serverConfig(server)
	if !ok {
		return
	}
	identity, cacheable := mcpToolCatalogIdentity(server, &config)
	if !cacheable {
		return
	}
	service.toolCatalogCacheStore().Drop(identity)
}

func helperStarts(t *testing.T, logPath string) int {
	t.Helper()
	return mcpSharingLogCount(t, logPath, "start")
}

// TestToolCatalogCacheWarmsNewChatsLikeRust covers the payoff of the
// process-scoped cache: a second chat, which never connected to the server,
// satisfies startup from the catalog the first chat published, so the helper is
// not launched again until that chat actually calls a tool.
func TestToolCatalogCacheWarmsNewChatsLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	server := "cached-warm-chat"
	now := time.Now()

	chatA := newCatalogCacheService(t, server, executable, logPath, "shared", func() time.Time { return now })
	response, err := chatA.ListStatusChecked(&MCPListServerStatusParams{})
	if err != nil {
		t.Fatalf("chat A ListStatusChecked() error = %v", err)
	}
	status := statusForServer(t, response, server)
	if len(status.Tools) == 0 {
		t.Fatal("chat A published no tools, so there is nothing to cache")
	}
	published := helperStarts(t, logPath)
	if published == 0 {
		t.Fatal("chat A did not start the helper")
	}

	// A brand-new chat: new service, no connection, no local catalog.
	chatB := newCatalogCacheService(t, server, executable, logPath, "shared", func() time.Time { return now })
	cached, err := chatB.ListStatusChecked(&MCPListServerStatusParams{})
	if err != nil {
		t.Fatalf("chat B ListStatusChecked() error = %v", err)
	}
	if starts := helperStarts(t, logPath); starts != published {
		t.Fatalf("helper starts = %d after chat B's status read, want %d: the warm catalog must keep the server dormant", starts, published)
	}
	cachedStatus := statusForServer(t, cached, server)
	if len(cachedStatus.Tools) == 0 {
		t.Fatalf("chat B tools = %#v, want the cached catalog", cachedStatus.Tools)
	}
	if cachedStatus.State != MCPServerReady {
		t.Fatalf("chat B state = %q, want ready from the cached catalog", cachedStatus.State)
	}

	// First actual use connects.
	config, _ := chatB.serverConfig(server)
	client := chatB.stdioClientForServer(server, &config)
	var result MCPToolCallResponse
	if err := client.CallWithOptions(&stdioCallOptions{ServerName: server}, "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{}}, &result); err != nil {
		t.Fatalf("chat B first call error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Fatalf("chat B first call result = %#v", result.Content)
	}
	if starts := helperStarts(t, logPath); starts <= published {
		t.Fatalf("helper starts = %d after chat B's first call, want a new process", starts)
	}
}

// TestToolCatalogCacheExpiresAfterTTLLikeRust proves the cache is a hint, not a
// promise: once the snapshot is older than the TTL a new chat starts the server
// eagerly again.
func TestToolCatalogCacheExpiresAfterTTLLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	server := "cached-expiry"
	publishedAt := time.Now()

	chatA := newCatalogCacheService(t, server, executable, logPath, "shared", func() time.Time { return publishedAt })
	if _, err := chatA.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("chat A ListStatusChecked() error = %v", err)
	}
	published := helperStarts(t, logPath)
	if published == 0 {
		t.Fatal("chat A did not start the helper")
	}

	stale := publishedAt.Add(toolCatalogCacheTTL + time.Minute)
	chatB := newCatalogCacheService(t, server, executable, logPath, "shared", func() time.Time { return stale })
	if _, err := chatB.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("chat B ListStatusChecked() error = %v", err)
	}
	if starts := helperStarts(t, logPath); starts <= published {
		t.Fatalf("helper starts = %d after an expired cache, want an eager start", starts)
	}
}

// TestToolCatalogCacheHonorsServerOptOutLikeRust covers the server-side opt-out:
// a helper that answers `{"codex/tool-catalog-cache": {"cacheable": false}}` is
// never served from the cache, and the entry stays disabled.
func TestToolCatalogCacheHonorsServerOptOutLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	server := "cached-opt-out"
	now := time.Now()

	chatA := newCatalogCacheService(t, server, executable, logPath, "cache-opt-out", func() time.Time { return now })
	if _, err := chatA.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("chat A ListStatusChecked() error = %v", err)
	}
	published := helperStarts(t, logPath)
	config, _ := chatA.serverConfig(server)
	identity, cacheable := mcpToolCatalogIdentity(server, &config)
	if !cacheable {
		t.Fatal("server should have a cache identity")
	}
	entry := chatA.toolCatalogCacheStore().Entry(identity)
	if entry == nil || !entry.Disabled() {
		t.Fatal("the opt-out capability must disable the cache entry")
	}

	chatB := newCatalogCacheService(t, server, executable, logPath, "cache-opt-out", func() time.Time { return now })
	if _, err := chatB.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("chat B ListStatusChecked() error = %v", err)
	}
	if starts := helperStarts(t, logPath); starts <= published {
		t.Fatalf("helper starts = %d, want chat B to start the opted-out server", starts)
	}
}

// TestToolCatalogCacheGenerationOrderingLikeRust covers the two generations the
// component keeps: only newer fetch generations publish, a modern discovery
// rules out an older catalog for discovery clients while legacy clients may
// still reuse it, and a legacy publish never advances the modern generation
// (Rust #60037).
func TestToolCatalogCacheGenerationOrderingLikeRust(t *testing.T) {
	now := time.Now()
	tool := MCPToolInfo{Name: "ask", InputSchema: map[string]any{"type": "object"}}

	t.Run("only newer fetch generations publish", func(t *testing.T) {
		entry := &ToolCatalogCacheEntry{}
		first := entry.BeginFetch(now)
		if first == 0 {
			t.Fatal("BeginFetch() = 0, want a monotonic generation")
		}
		if !entry.PublishIfNewest(first, []MCPToolInfo{tool}, nil, now) {
			t.Fatal("publish of the first catalog was rejected")
		}
		if entry.PublishIfNewest(first, []MCPToolInfo{tool}, nil, now) {
			t.Fatal("a repeated generation must not republish")
		}
		if _, ok := entry.CurrentTools(now); !ok {
			t.Fatal("published catalog is not readable")
		}
		if _, ok := entry.CurrentTools(now.Add(toolCatalogCacheTTL + time.Second)); ok {
			t.Fatal("an expired catalog is still readable")
		}
		entry.Disable()
		if _, ok := entry.CurrentTools(now); ok {
			t.Fatal("a disabled entry must not serve a catalog")
		}
		if entry.PublishIfNewest(entry.BeginFetch(now), []MCPToolInfo{tool}, nil, now) {
			t.Fatal("a disabled entry must not accept new catalogs")
		}
	})

	t.Run("modern discovery rules out the older catalog for discovery clients", func(t *testing.T) {
		entry := &ToolCatalogCacheEntry{}
		legacy := entry.BeginFetch(now)
		if !entry.PublishIfNewest(legacy, []MCPToolInfo{tool}, nil, now) {
			t.Fatal("publish of the legacy catalog was rejected")
		}
		modern := entry.BeginFetch(now)
		entry.RecordModernDiscovery(modern, now)
		if _, ok := entry.currentTools(now, true); ok {
			t.Fatal("a discovery client must not reuse a catalog older than its modern handshake")
		}
		if _, ok := entry.CurrentTools(now); !ok {
			t.Fatal("a legacy client may still reuse the older catalog")
		}
		// The catalog published at the modern generation itself is reusable.
		if !entry.PublishIfNewest(modern, []MCPToolInfo{tool}, nil, now) {
			t.Fatal("publish at the modern generation was rejected")
		}
		if _, ok := entry.currentTools(now, true); !ok {
			t.Fatal("a discovery client must reuse a catalog published at its own handshake")
		}
	})

	t.Run("legacy publish does not advance the modern generation", func(t *testing.T) {
		entry := &ToolCatalogCacheEntry{}
		if !entry.PublishIfNewest(entry.BeginFetch(now), []MCPToolInfo{tool}, nil, now) {
			t.Fatal("publish of the first catalog was rejected")
		}
		if got := entry.modernDiscoveryGeneration(); got != 0 {
			t.Fatalf("modern generation = %d after a legacy publish, want 0", got)
		}
		modern := entry.BeginFetch(now)
		entry.RecordModernDiscovery(modern, now)
		if !entry.PublishIfNewest(entry.BeginFetch(now), []MCPToolInfo{tool}, nil, now) {
			t.Fatal("a legacy publish newer than the handshake was rejected")
		}
		if got := entry.modernDiscoveryGeneration(); got != modern {
			t.Fatalf("modern generation = %d after a newer legacy publish, want %d", got, modern)
		}
		if _, ok := entry.currentTools(now, true); !ok {
			t.Fatal("a catalog newer than the modern handshake must stay reusable")
		}
	})
}

// toolCatalogGenerations reads the two generations the entry keeps for one
// server, so a test can tell a published catalog from a recorded handshake.
func toolCatalogGenerations(t *testing.T, service *MCPService, server string) (uint64, uint64) {
	t.Helper()
	config, ok := service.serverConfig(server)
	if !ok {
		t.Fatalf("server %q is not configured", server)
	}
	identity, cacheable := mcpToolCatalogIdentity(server, &config)
	if !cacheable {
		t.Fatalf("server %q has no cache identity", server)
	}
	entry := service.toolCatalogCacheStore().Entry(identity)
	if entry == nil {
		t.Fatalf("server %q has no cache entry", server)
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.lastCatalogGeneration, entry.lastModernDiscoveryGeneration
}

// TestToolCatalogCacheModernHandshakeAdvancesModernGenerationLikeRust proves the
// service records the handshake rather than only carrying the field: a
// discovery connection publishes its catalog at the generation it negotiated,
// and a later legacy connection publishing a newer catalog leaves the modern
// generation untouched.
func TestToolCatalogCacheModernHandshakeAdvancesModernGenerationLikeRust(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "starts")
	server := "cached-modern-handshake"
	now := time.Now()

	modern := newCatalogCacheServiceWithProtocol(t, server, executable, logPath, "shared", MCPProtocolDiscoverLocalStdio, func() time.Time { return now })
	config, _ := modern.serverConfig(server)
	if result := modern.inventoryStatusForConfig(0, server, &config, MCPServerStatus{}, false, ""); result.Err != nil {
		t.Fatalf("modern chat inventory error = %v", result.Err)
	}
	catalog, modernGen := toolCatalogGenerations(t, modern, server)
	if catalog == 0 {
		t.Fatal("the modern chat published no catalog generation")
	}
	if modernGen != catalog {
		t.Fatalf("modern generation = %d, want the published generation %d", modernGen, catalog)
	}

	legacy := newCatalogCacheServiceWithProtocol(t, server, executable, logPath, "shared", MCPProtocolLegacy, func() time.Time { return now })
	legacyConfig, _ := legacy.serverConfig(server)
	if result := legacy.inventoryStatusForConfig(0, server, &legacyConfig, MCPServerStatus{}, false, ""); result.Err != nil {
		t.Fatalf("legacy chat inventory error = %v", result.Err)
	}
	catalogAfter, modernAfter := toolCatalogGenerations(t, legacy, server)
	if catalogAfter <= catalog {
		t.Fatalf("legacy catalog generation = %d, want it to advance past %d", catalogAfter, catalog)
	}
	if modernAfter != modernGen {
		t.Fatalf("modern generation = %d after a legacy publish, want it to stay %d", modernAfter, modernGen)
	}
}
