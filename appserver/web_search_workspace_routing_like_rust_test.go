package appserver

import (
	"context"
	"net/http"
	"testing"

	"codex_go/config"
	"codex_go/turn"
)

// TestStandaloneWebSearchUsesWorkspaceRoutingLikeRust covers Rust #58140: the
// standalone web search client routes through the thread's discovered workspace
// backend, carries the account-routing header, and never follows a redirect.
func TestStandaloneWebSearchUsesWorkspaceRoutingLikeRust(t *testing.T) {
	router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/check" {
			t.Fatalf("accounts check path = %q", r.URL.Path)
		}
		writeJSON(t, w, map[string]any{
			"accounts": []any{map[string]any{
				"id":                       "workspace",
				"workspace_backend_origin": "https://gov.chatgpt.com",
				"account_routing_override": "us",
			}},
		})
	})
	cfg := &config.Config{Values: map[string]any{
		"web_search": "cached",
		"features": map[string]any{
			"standalone_web_search": true,
		},
	}}
	options, err := router.webSearchOptionsForTurn(cfg, &turn.TurnStartParams{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("webSearchOptionsForTurn() error = %v", err)
	}
	if options == nil {
		t.Fatal("standalone web search was not registered")
	}
	// Rust resolves the routed provider inside WebSearchTool::run, so turn
	// preparation must not contact the routing backend; the resolved provider
	// appears only when the search request is prepared.
	if options.PrepareRequest == nil {
		t.Fatal("a routed provider must resolve routing per search request")
	}
	prepared, err := options.PrepareRequest(context.Background())
	if err != nil {
		t.Fatalf("PrepareRequest() error = %v", err)
	}
	if got := prepared.Provider.BaseURL; got != "https://gov.chatgpt.com/backend-api/codex" {
		t.Fatalf("provider base URL = %q, want the workspace backend", got)
	}
	if got := prepared.Provider.Headers.Get("x-openai-account-routing-override"); got != "us" {
		t.Fatalf("account routing header = %q", got)
	}
	if !prepared.RejectRedirects {
		t.Fatal("a routed search client must reject redirects")
	}
	if got := options.Provider.BaseURL; got == prepared.Provider.BaseURL {
		t.Fatal("turn preparation must keep the bootstrap provider unrouted")
	}
}

// TestStandaloneWebSearchPropagatesRoutingDiscoveryFailureLikeRust covers the
// unroutable case: when workspace routing discovery fails, the standalone
// search client is not built with a silently unrouted origin.
func TestStandaloneWebSearchPropagatesRoutingDiscoveryFailureLikeRust(t *testing.T) {
	router := workspaceRoutingRouter(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{"accounts": []any{map[string]any{"id": "workspace"}}})
	})
	cfg := &config.Config{Values: map[string]any{
		"web_search": "cached",
		"features": map[string]any{
			"standalone_web_search": true,
		},
	}}
	options, err := router.webSearchOptionsForTurn(cfg, &turn.TurnStartParams{ThreadID: "thread-1"})
	if err != nil {
		t.Fatalf("turn preparation must not fail on routing discovery: %v", err)
	}
	if options == nil || options.PrepareRequest == nil {
		t.Fatal("a routed provider must resolve routing per search request")
	}
	if _, err := options.PrepareRequest(context.Background()); err == nil {
		t.Fatal("a broken workspace routing discovery must fail the search request")
	}
}
