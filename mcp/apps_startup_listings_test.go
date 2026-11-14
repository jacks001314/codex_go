package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAppsStartupListingsShareOnlyOverlap(t *testing.T) {
	var listings appsStartupListings
	started, release, ownerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var calls atomic.Int32
	fetch := func(ctx context.Context) ([]MCPToolInfo, json.RawMessage, error) {
		if calls.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
		}
		return []MCPToolInfo{{Name: "lookup"}}, json.RawMessage(`{"v":1}`), nil
	}
	go func() {
		defer close(ownerDone)
		_, _, err := listings.fetch(ctx, "scope", fetch)
		if err != nil {
			t.Error(err)
		}
	}()
	<-started
	// Synchronize the waiter at its context check while the owner is in flight.
	waiterCtx := &observedStartupContext{Context: ctx, checked: make(chan struct{}, 2)}
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		tools, meta, err := listings.fetch(waiterCtx, "scope", fetch)
		if err != nil || len(tools) != 1 || string(meta) != `{"v":1}` {
			t.Errorf("peer=%v %s %v", tools, meta, err)
		}
	}()
	<-waiterCtx.checked
	// The second context observation comes from Done(), after finding pending.
	<-waiterCtx.checked
	close(release)
	<-ownerDone
	<-peerDone
	if calls.Load() != 1 {
		t.Fatalf("overlapping calls=%d", calls.Load())
	}
	_, _, err := listings.fetch(ctx, "scope", fetch)
	if err != nil || calls.Load() != 2 {
		t.Fatalf("later startup did not refetch: %d %v", calls.Load(), err)
	}
}

type observedStartupContext struct {
	context.Context
	checked chan struct{}
}

func (c *observedStartupContext) Err() error {
	select {
	case c.checked <- struct{}{}:
	default:
	}
	return c.Context.Err()
}
func (c *observedStartupContext) Done() <-chan struct{} {
	select {
	case c.checked <- struct{}{}:
	default:
	}
	return c.Context.Done()
}

func TestAppsStartupListingsFailureTimeoutAndIsolation(t *testing.T) {
	var listings appsStartupListings
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, ownerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(ownerDone)
		_, _, _ = listings.fetch(ctx, "one", func(ctx context.Context) ([]MCPToolInfo, json.RawMessage, error) {
			close(started)
			<-ctx.Done()
			return nil, nil, ctx.Err()
		})
	}()
	<-started
	otherCalls := 0
	own := func(context.Context) ([]MCPToolInfo, json.RawMessage, error) {
		otherCalls++
		return []MCPToolInfo{{Name: "own"}}, nil, nil
	}
	if _, _, err := listings.fetch(context.Background(), "two", own); err != nil {
		t.Fatal(err)
	}
	if otherCalls != 1 {
		t.Fatal("different scope was blocked")
	}
	deadline, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if _, _, err := listings.fetch(deadline, "one", own); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting error=%v", err)
	}
	if otherCalls != 1 {
		t.Fatal("expired waiter performed fetch")
	}
	waiterCtx := &observedStartupContext{Context: context.Background(), checked: make(chan struct{}, 2)}
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		_, _, err := listings.fetch(waiterCtx, "one", own)
		if err != nil {
			t.Error(err)
		}
	}()
	<-waiterCtx.checked
	<-waiterCtx.checked
	cancel()
	<-ownerDone
	<-peerDone
	if otherCalls != 2 {
		t.Fatalf("waiting peer did not recover: %d", otherCalls)
	}
}

func TestAppsStartupHTTPWaitInitializesIndependentClient(t *testing.T) {
	server := newClientCatalogTestServer(t, "lookup")
	release := make(chan struct{})
	server.mu.Lock()
	server.blockLists = release
	server.mu.Unlock()
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	config := &ServerConfig{URL: server.server.URL, Enabled: true, StartupTimeout: time.Second}
	first, second := newMCPHTTPClient(config), newMCPHTTPClient(config)
	var listings appsStartupListings
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := listAppsStartupTools(ctx, first, nil, "scope", &listings); done <- err }()
	select {
	case <-server.listStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	wait, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	_, _, err := listAppsStartupTools(wait, second, nil, "scope", &listings)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait error=%v", err)
	}
	if !second.isInitialized() {
		t.Fatal("waiting client did not initialize its own connection")
	}
	if server.listCount() != 1 {
		t.Fatalf("waiter made duplicate tools/list: %d", server.listCount())
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The waiting connection remains usable independently of the owner's catalog.
	var reply map[string]any
	if err := second.Call("tools/call", map[string]any{"name": "lookup", "arguments": map[string]any{}}, &reply); err != nil {
		t.Fatal(err)
	}
}

func TestAppsStartupDiscoveryScopeSeparatesRuntimeIdentities(t *testing.T) {
	service := NewMCPService(nil)
	config := CodexAppsServerConfig("https://example.test", "sku", &RuntimeAuth{AccountID: "account", ChatGPTUserID: "user"})
	config.CodexHome = t.TempDir()
	original := service.appsStartupDiscoveryScope(CodexAppsServerName, &config, nil)
	if original == "" {
		t.Fatal("trusted runtime identity did not produce scope")
	}
	same := cloneServerConfig(&config)
	if service.appsStartupDiscoveryScope(CodexAppsServerName, &same, nil) != original {
		t.Fatal("equivalent config differs")
	}
	for _, change := range []func(*ServerConfig){
		func(c *ServerConfig) { c.appsDiscoveryOwner = "other" },
		func(c *ServerConfig) { c.CodexHome += "-other" },
		func(c *ServerConfig) { c.EnvironmentID = "other" },
		func(c *ServerConfig) { c.URL += "/other" },
		func(c *ServerConfig) { c.RequiresReadOnlyTools = true },
	} {
		changed := cloneServerConfig(&config)
		change(&changed)
		if service.appsStartupDiscoveryScope(CodexAppsServerName, &changed, nil) == original {
			t.Fatal("different discovery scope shared")
		}
	}
	config.appsDiscoveryOwner = ""
	if service.appsStartupDiscoveryScope(CodexAppsServerName, &config, nil) != "" {
		t.Fatal("missing runtime identity shared")
	}
}

func TestAppsStartupHTTPOverlapSharesCatalogButNotConnections(t *testing.T) {
	server := newClientCatalogTestServer(t, "lookup")
	config := &ServerConfig{URL: server.url(), Enabled: true, StartupTimeout: time.Second}
	first, second := newMCPHTTPClient(config), newMCPHTTPClient(config)
	defer first.Close()
	defer second.Close()
	// Warm only the handshakes, without tools/list, so the waiter barrier below
	// observes the coordinator rather than HTTP initialization internals.
	for _, client := range []*httpClient{first, second} {
		var reply map[string]any
		if err := client.Call("tools/call", map[string]any{"name": "lookup"}, &reply); err != nil {
			t.Fatal(err)
		}
	}
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	server.setBlockLists(release)
	var listings appsStartupListings
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ownerDone := make(chan error, 1)
	go func() { _, _, err := listAppsStartupTools(ctx, first, nil, "same", &listings); ownerDone <- err }()
	select {
	case <-server.listStarted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	waiter := &observedStartupContext{Context: ctx, checked: make(chan struct{}, 2)}
	peerDone := make(chan error, 1)
	go func() {
		tools, _, err := listAppsStartupTools(waiter, second, nil, "same", &listings)
		if err == nil && (len(tools) != 1 || tools[0].Name != "lookup") {
			err = errors.New("shared catalog mismatch")
		}
		peerDone <- err
	}()
	<-waiter.checked
	<-waiter.checked
	unblock()
	if err := <-ownerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-peerDone; err != nil {
		t.Fatal(err)
	}
	if server.listCount() != 1 {
		t.Fatalf("lists=%d", server.listCount())
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	var reply map[string]any
	if err := second.Call("tools/call", map[string]any{"name": "lookup"}, &reply); err != nil {
		t.Fatal(err)
	}
	if _, _, err := listAppsStartupTools(ctx, second, nil, "same", &listings); err != nil {
		t.Fatal(err)
	}
	if server.listCount() != 2 {
		t.Fatal("later listing did not refetch")
	}
}

func TestAppsStartupHTTPServerOptOutBypassesPendingListing(t *testing.T) {
	server := newClientCatalogTestServer(t, "own")
	client := newMCPHTTPClient(&ServerConfig{URL: server.url(), Enabled: true, StartupTimeout: time.Second})
	defer client.Close()
	var reply map[string]any
	if err := client.Call("tools/call", map[string]any{"name": "own"}, &reply); err != nil {
		t.Fatal(err)
	}
	client.capabilitiesMu.Lock()
	client.serverCapabilities = json.RawMessage(`{"experimental":{"codex/tool-catalog-cache":{"cacheable":false}}}`)
	client.capabilitiesMu.Unlock()
	listings := appsStartupListings{pending: map[string]*appsStartupListing{"scope": {done: make(chan struct{})}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tools, _, err := listAppsStartupTools(ctx, client, nil, "scope", &listings)
	if err != nil || len(tools) != 1 || tools[0].Name != "own" {
		t.Fatalf("opt-out tools=%v error=%v", tools, err)
	}
	if server.listCount() != 1 {
		t.Fatal("opt-out skipped its own listing")
	}
}

func TestAppsStartupSnapshotDoesNotShareMutableSchemas(t *testing.T) {
	original := []MCPToolInfo{{InputSchema: map[string]any{"properties": map[string]any{"name": "original"}}, Meta: map[string]any{"label": "original"}, Icons: []any{map[string]any{"src": "original"}}}}
	cloned := cloneAppsStartupTools(original)
	cloned[0].InputSchema["properties"].(map[string]any)["name"] = "changed"
	cloned[0].Meta.(map[string]any)["label"] = "changed"
	cloned[0].Icons[0].(map[string]any)["src"] = "changed"
	if original[0].InputSchema["properties"].(map[string]any)["name"] != "original" || original[0].Meta.(map[string]any)["label"] != "original" || original[0].Icons[0].(map[string]any)["src"] != "original" {
		t.Fatal("client modified shared startup snapshot")
	}
}

func TestAppsStartupServicesHonorInitializeOptOut(t *testing.T) {
	for _, optOut := range []bool{false, true} {
		t.Run(fmt.Sprint(optOut), func(t *testing.T) {
			server := newClientCatalogTestServer(t, "lookup")
			server.optOut = optOut
			release := make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			server.setBlockLists(release)
			first := newClientCatalogTestService(t, server, nil)
			second := newClientCatalogTestService(t, server, nil)
			config := ServerConfig{URL: server.url(), Enabled: true, StartupTimeout: 150 * time.Millisecond, appsDiscoveryOwner: "trusted-test-owner", CodexHome: t.TempDir()}
			// Exercise the production discovery entry point with host-owned registration
			// status; the two services still create distinct clients.
			status := MCPServerStatus{hostOwnedApps: true}
			ownerDone := make(chan error, 1)
			ownerConfig := config
			ownerConfig.StartupTimeout = 2 * time.Second
			go func() {
				_, err := first.discoverToolCatalogForStatus(CodexAppsServerName, &ownerConfig, "", status)
				ownerDone <- err
			}()
			select {
			case <-server.listStarted:
			case <-time.After(time.Second):
				t.Fatal("owner did not list")
			}
			inventory, err := second.discoverToolCatalogForStatus(CodexAppsServerName, &config, "", status)
			if optOut {
				if err != nil || len(inventory.Tools) != 1 {
					t.Fatalf("opt-out inventory=%v error=%v", inventory, err)
				}
				if server.listCount() != 2 {
					t.Fatalf("opt-out list count=%d", server.listCount())
				}
			} else {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("waiting error=%v", err)
				}
				if server.listCount() != 1 {
					t.Fatalf("shared list count=%d", server.listCount())
				}
			}
			unblock()
			<-ownerDone
			if first.httpClients[CodexAppsServerName].client == second.httpClients[CodexAppsServerName].client {
				t.Fatal("services shared a connection")
			}
		})
	}
}

func TestAppsConnectionIdentityChangesWithTrustedOwner(t *testing.T) {
	first := CodexAppsServerConfig("https://example.test", "", &RuntimeAuth{AccountID: "first", ChatGPTUserID: "user"})
	second := CodexAppsServerConfig("https://example.test", "", &RuntimeAuth{AccountID: "second", ChatGPTUserID: "user"})
	if mcpConnectionCacheKey(&first, false) == mcpConnectionCacheKey(&second, false) {
		t.Fatal("account change reuses old Apps connection")
	}
}

type testAppsRuntimeProvider struct{}

func (testAppsRuntimeProvider) HeadersForDestination(context.Context, string) (http.Header, error) {
	return http.Header{}, nil
}

func TestAppsDynamicRuntimeIdentityDoesNotShareStaticOwner(t *testing.T) {
	config := CodexAppsServerConfig("https://example.test", "", &RuntimeAuth{AccountID: "account", ChatGPTUserID: "user", RuntimeProvider: testAppsRuntimeProvider{}})
	if config.appsDiscoveryOwner != "" {
		t.Fatal("dynamic runtime retained static discovery owner")
	}
	service := NewMCPService(nil)
	if service.appsStartupDiscoveryScope(CodexAppsServerName, &config, nil) != "" {
		t.Fatal("dynamic runtime shared by static account")
	}
}
