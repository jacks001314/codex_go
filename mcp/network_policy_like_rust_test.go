package mcp

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

	"codex_go/sandbox"
)

type mcpTestNetworkPolicyProvider struct {
	mu       sync.Mutex
	calls    int
	metadata *NetworkPolicyMetadata
	err      error
}

func (p *mcpTestNetworkPolicyProvider) NetworkPolicyMetadataForCall(profile *sandbox.PermissionProfile, environmentID string) (*NetworkPolicyMetadata, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	if p.metadata != nil {
		return p.metadata, nil
	}
	return &NetworkPolicyMetadata{
		Enabled:        true,
		Mode:           MCPNetworkModeLimited,
		AllowedDomains: []string{environmentID},
	}, nil
}

func (p *mcpTestNetworkPolicyProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func mcpNetworkPolicyTestMetadata() *NetworkPolicyMetadata {
	return &NetworkPolicyMetadata{
		Enabled:                 true,
		Mode:                    MCPNetworkModeLimited,
		AllowedDomains:          []string{"api.example.com"},
		DeniedDomains:           []string{"evil.example.com"},
		HardDenyAllowlistMisses: true,
	}
}

// TestMCPNetworkPolicyCapabilityOnlyAdvertisedToTrustedChatGPTOriginsLikeRust
// covers Rust #56278: the capability requires a policy source and a trusted
// ChatGPT origin, and it is advertised as experimental rather than as an
// extension.
func TestMCPNetworkPolicyCapabilityOnlyAdvertisedToTrustedChatGPTOriginsLikeRust(t *testing.T) {
	origins := []struct {
		name      string
		serverURL string
		baseURL   string
		want      bool
	}{
		{name: "chatgpt", serverURL: "https://chatgpt.com/backend-api/codex/mcp", want: true},
		{name: "gov", serverURL: "https://gov.chatgpt.com/backend-api/codex/mcp", want: true},
		{name: "staging", serverURL: "https://chatgpt-staging.com/backend-api/codex/mcp", baseURL: "https://chatgpt-staging.com/backend-api", want: true},
		{name: "staging subdomain", serverURL: "https://team.chatgpt-staging.com/mcp", baseURL: "https://team.chatgpt-staging.com/backend-api", want: true},
		{name: "staging origin mismatch", serverURL: "https://other.chatgpt-staging.com/mcp", baseURL: "https://chatgpt-staging.com/backend-api"},
		{name: "lookalike host", serverURL: "https://chatgpt.com.evil.test/mcp"},
		{name: "unrelated origin", serverURL: "https://mcp.example.com/mcp"},
		{name: "non http scheme", serverURL: "ftp://chatgpt.com/mcp"},
		{name: "unparseable", serverURL: "not a url"},
	}
	for _, test := range origins {
		t.Run(test.name, func(t *testing.T) {
			config := &ServerConfig{URL: test.serverURL, Enabled: true}
			if got := mcpIsTrustedChatGPTMCPServer(config, test.baseURL); got != test.want {
				t.Fatalf("mcpIsTrustedChatGPTMCPServer(%q) = %t, want %t", test.serverURL, got, test.want)
			}
		})
	}
	if mcpIsTrustedChatGPTMCPServer(&ServerConfig{Command: "helper"}, "") {
		t.Fatal("a stdio server was treated as a trusted ChatGPT origin")
	}

	trusted := &ServerConfig{URL: "https://chatgpt.com/backend-api/codex/mcp", Enabled: true}
	if (&MCPService{}).networkPolicyAdvertisedForServer(trusted) {
		t.Fatal("the capability was advertised without a policy source")
	}
	provider := &mcpTestNetworkPolicyProvider{}
	service := NewMCPService(&RuntimeConfig{NetworkPolicyMetadata: provider})
	if !service.networkPolicyAdvertisedForServer(trusted) {
		t.Fatal("trusted origin did not advertise the capability")
	}
	if service.networkPolicyAdvertisedForServer(&ServerConfig{URL: "https://mcp.example.com/mcp"}) {
		t.Fatal("untrusted origin advertised the capability")
	}

	params := mcpClientInitializeParamsForFeatures(mcpClientFeatures{NetworkPolicy: true})
	capabilities, _ := params["capabilities"].(map[string]any)
	experimental, _ := capabilities["experimental"].(map[string]any)
	if _, ok := experimental[MCPNetworkPolicyMetaCapability]; !ok {
		t.Fatalf("capabilities = %#v, want the experimental network policy capability", capabilities)
	}
	if extensions, _ := capabilities["extensions"].(map[string]any); extensions != nil {
		if _, ok := extensions[MCPNetworkPolicyMetaCapability]; ok {
			t.Fatal("the network policy capability was advertised as an extension")
		}
	}
	fallback, _ := mcpClientInitializeParamsForFeatures(mcpClientFeatures{})["capabilities"].(map[string]any)
	if experimental, _ := fallback["experimental"].(map[string]any); experimental != nil {
		if _, ok := experimental[MCPNetworkPolicyMetaCapability]; ok {
			t.Fatal("the capability was advertised without the feature")
		}
	}
}

// TestMCPNetworkPolicyMetadataStripsForgedCallerMetaLikeRust covers Rust
// #56278: caller-supplied policy metadata never reaches a server, even when no
// policy source is configured, while caller metadata is preserved.
func TestMCPNetworkPolicyMetadataStripsForgedCallerMetaLikeRust(t *testing.T) {
	service := &MCPService{}
	resolver := service.mcpNetworkPolicyMetaForToolCall("remote", &ServerConfig{URL: "https://chatgpt.com/mcp"}, "", map[string]any{
		"callId":                       "call-1",
		MCPNetworkPolicyMetaCapability: map[string]any{"enabled": false},
	})
	meta, err := resolver(json.RawMessage(`{"experimental":{"codex/network-policy-meta":{}}}`))
	if err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	metaMap, _ := meta.(map[string]any)
	if _, ok := metaMap[MCPNetworkPolicyMetaCapability]; ok {
		t.Fatalf("forged policy metadata survived: %#v", metaMap)
	}
	if metaMap["callId"] != "call-1" {
		t.Fatalf("caller metadata = %#v, want callId preserved", metaMap)
	}
}

// TestMCPNetworkPolicyMetadataRequiresNegotiatedSessionLikeRust covers Rust
// #56278: metadata is attached only when the sending session advertises the
// capability, and one snapshot is reused for the whole call.
func TestMCPNetworkPolicyMetadataRequiresNegotiatedSessionLikeRust(t *testing.T) {
	provider := &mcpTestNetworkPolicyProvider{metadata: mcpNetworkPolicyTestMetadata()}
	service := NewMCPService(&RuntimeConfig{
		NetworkPolicyMetadata: provider,
		PermissionProfile:     &sandbox.PermissionProfile{Disabled: true},
	})
	resolver := service.mcpNetworkPolicyMetaForToolCall("remote", &ServerConfig{URL: "https://chatgpt.com/mcp"}, "env-1", map[string]any{"callId": "call-1"})

	plain, err := resolver(json.RawMessage(`{"tools":{}}`))
	if err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	if policy, ok := plain.(map[string]any)[MCPNetworkPolicyMetaCapability]; ok {
		t.Fatalf("policy metadata attached without negotiation: %#v", policy)
	}
	if provider.callCount() != 0 {
		t.Fatalf("provider calls = %d, want 0 before negotiation", provider.callCount())
	}

	supporting := json.RawMessage(`{"experimental":{"codex/network-policy-meta":{}}}`)
	first, err := resolver(supporting)
	if err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	firstMap, _ := first.(map[string]any)
	policy, _ := firstMap[MCPNetworkPolicyMetaCapability].(map[string]any)
	if policy["enabled"] != true || policy["mode"] != "limited" {
		t.Fatalf("policy = %#v", policy)
	}
	if domains, _ := policy["allowedDomains"].([]any); len(domains) != 1 || domains[0] != "api.example.com" {
		t.Fatalf("allowedDomains = %#v", policy["allowedDomains"])
	}
	if policy["hardDenyAllowlistMisses"] != true {
		t.Fatalf("hardDenyAllowlistMisses = %#v", policy["hardDenyAllowlistMisses"])
	}
	if firstMap["callId"] != "call-1" {
		t.Fatalf("caller metadata = %#v, want callId preserved", firstMap)
	}

	// A recovery retry re-resolves against new capabilities but reuses the
	// snapshot captured for this call.
	if _, err := resolver(json.RawMessage(`{"tools":{}}`)); err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	second, err := resolver(supporting)
	if err != nil {
		t.Fatalf("resolver() error = %v", err)
	}
	secondPolicy, _ := second.(map[string]any)[MCPNetworkPolicyMetaCapability].(map[string]any)
	if secondPolicy["mode"] != "limited" {
		t.Fatalf("second policy = %#v", secondPolicy)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want one snapshot per call", provider.callCount())
	}
}

// TestMCPNetworkPolicyMetadataFailsBeforeSendingLikeRust covers Rust #56278:
// an unavailable policy source or a missing captured authority fails the call
// before any request is sent.
func TestMCPNetworkPolicyMetadataFailsBeforeSendingLikeRust(t *testing.T) {
	supporting := json.RawMessage(`{"experimental":{"codex/network-policy-meta":{}}}`)
	failing := &mcpTestNetworkPolicyProvider{err: errors.New("policy source unavailable")}
	service := NewMCPService(&RuntimeConfig{
		NetworkPolicyMetadata: failing,
		PermissionProfile:     &sandbox.PermissionProfile{Disabled: true},
	})
	resolver := service.mcpNetworkPolicyMetaForToolCall("remote", &ServerConfig{URL: "https://chatgpt.com/mcp"}, "", nil)
	if _, err := resolver(supporting); err == nil || !strings.Contains(err.Error(), "policy source unavailable") {
		t.Fatalf("resolver() error = %v, want the provider failure", err)
	}

	noAuthority := NewMCPService(&RuntimeConfig{NetworkPolicyMetadata: failing})
	resolver = noAuthority.mcpNetworkPolicyMetaForToolCall("remote", &ServerConfig{URL: "https://chatgpt.com/mcp"}, "", nil)
	if _, err := resolver(supporting); err == nil || !strings.Contains(err.Error(), "permission authority") {
		t.Fatalf("resolver() error = %v, want the missing-authority failure", err)
	}
}

// TestMCPNetworkPolicyMetadataFollowsRecoveredSessionLikeRust covers Rust
// #56278: the first session does not negotiate policy support, a recovery
// retry negotiates it, and the retried call carries the policy metadata built
// from the same call authority.
func TestMCPNetworkPolicyMetadataFollowsRecoveredSessionLikeRust(t *testing.T) {
	var (
		mu         sync.Mutex
		handshakes int
		callMetas  []map[string]any
		advertised bool
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int64 `json:"id"`
			Method string
			Params struct {
				Meta         map[string]any `json:"_meta"`
				Capabilities map[string]any `json:"capabilities"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		switch request.Method {
		case "initialize":
			mu.Lock()
			handshakes++
			current := handshakes
			mu.Unlock()
			experimental, _ := request.Params.Capabilities["experimental"].(map[string]any)
			if _, ok := experimental[MCPNetworkPolicyMetaCapability]; ok {
				mu.Lock()
				advertised = true
				mu.Unlock()
			}
			w.Header().Set(mcpHTTPSessionIDHeader, fmt.Sprintf("session-%d", current))
			capabilities := map[string]any{"tools": map[string]any{}}
			if current > 1 {
				capabilities["experimental"] = map[string]any{MCPNetworkPolicyMetaCapability: map[string]any{}}
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    capabilities,
				"serverInfo":      map[string]string{"name": "policy-helper", "version": "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			mu.Lock()
			callMetas = append(callMetas, request.Params.Meta)
			expire := len(callMetas) == 1
			mu.Unlock()
			if expire {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ok"}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	// This test drives the transport directly so the recovery path runs against
	// the local mock server; the trusted-origin projection is covered by the
	// capability test above.
	client := newMCPHTTPClientWithCapabilities(&ServerConfig{
		URL:                   server.URL + "/mcp",
		Enabled:               true,
		RequiresReadOnlyTools: true,
	}, mcpClientFeatures{NetworkPolicy: true}, nil)
	defer client.Close()

	provider := &mcpTestNetworkPolicyProvider{metadata: mcpNetworkPolicyTestMetadata()}
	resolver := &mcpNetworkPolicyMetaResolver{
		base:          map[string]any{"callId": "call-1", mcpReadOnlyToolsMetaKey: true},
		advertised:    true,
		provider:      provider,
		profile:       &sandbox.PermissionProfile{Disabled: true},
		environmentID: "env-1",
	}
	response, err := callMCPHTTPToolWithClient(context.Background(), client, "remote", "", "", "", nil, nil, nil, "echo",
		map[string]any{"message": "hi"}, resolver.base, resolver.metaFor)
	if err != nil {
		t.Fatalf("callMCPHTTPToolWithClient() error = %v", err)
	}
	if len(response.Content) != 1 || response.Content[0].Text != "ok" {
		t.Fatalf("response = %#v", response.Content)
	}

	mu.Lock()
	metas := append([]map[string]any(nil), callMetas...)
	handshakeCount := handshakes
	clientAdvertised := advertised
	mu.Unlock()
	if !clientAdvertised {
		t.Fatal("the client did not advertise the network policy capability during initialize")
	}
	if handshakeCount != 2 {
		t.Fatalf("handshakes = %d, want an initial and a recovered session", handshakeCount)
	}
	if len(metas) != 2 {
		t.Fatalf("tools/call attempts = %d, want 2", len(metas))
	}
	for i, meta := range metas {
		if meta["callId"] != "call-1" {
			t.Fatalf("attempt %d callId = %#v", i, meta["callId"])
		}
		if meta[mcpReadOnlyToolsMetaKey] != true {
			t.Fatalf("attempt %d read-only marker = %#v", i, meta[mcpReadOnlyToolsMetaKey])
		}
	}
	if _, ok := metas[0][MCPNetworkPolicyMetaCapability]; ok {
		t.Fatalf("first attempt carried policy metadata: %#v", metas[0])
	}
	policy, _ := metas[1][MCPNetworkPolicyMetaCapability].(map[string]any)
	if policy["enabled"] != true || policy["mode"] != "limited" {
		t.Fatalf("recovered policy = %#v", policy)
	}
	if provider.callCount() != 1 {
		t.Fatalf("provider calls = %d, want one snapshot per call", provider.callCount())
	}
}
