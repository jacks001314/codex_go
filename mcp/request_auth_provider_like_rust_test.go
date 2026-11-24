package mcp

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// rotatingAuth is an in-process credential source whose owner and headers can
// change independently of the connection.
type rotatingAuth struct {
	mu      sync.Mutex
	owner   string
	token   string
	calls   int
	failErr error
}

func (a *rotatingAuth) OwnerKey() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failErr != nil {
		return "", a.failErr
	}
	return a.owner, nil
}

func (a *rotatingAuth) AddAuthHeaders(headers http.Header) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.failErr != nil {
		return "", a.failErr
	}
	headers.Set("Authorization", "Bearer "+a.token)
	return a.owner, nil
}

func (a *rotatingAuth) rotate(owner, token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.owner = owner
	a.token = token
}

// TestMcpRequestAuthRotatesHeadersOnOneConnectionLikeRust mirrors Rust #63532:
// the provider is asked for fresh headers on every request, and a credential
// refresh for the same owner keeps the underlying connection.
func TestMcpRequestAuthRotatesHeadersOnOneConnectionLikeRust(t *testing.T) {
	auth := &rotatingAuth{owner: "owner-1", token: "one"}
	var mu sync.Mutex
	var seen []string
	connections := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			mu.Lock()
			connections++
			mu.Unlock()
		}
	}
	server.Start()
	defer server.Close()

	config, err := SetMcpRequestAuthProvider(&ServerConfig{URL: server.URL, Enabled: true}, NewMcpRequestAuthProvider(auth))
	if err != nil {
		t.Fatalf("SetMcpRequestAuthProvider() error = %v", err)
	}
	client := newMCPHTTPClient(config)
	do := func() {
		request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("NewRequest() error = %v", err)
		}
		response, err := client.client.Do(request)
		if err != nil {
			t.Fatalf("Do() error = %v", err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}
	do()
	auth.rotate("owner-1", "two")
	do()

	mu.Lock()
	got := append([]string(nil), seen...)
	newConnections := connections
	mu.Unlock()
	if len(got) != 2 || got[0] != "Bearer one" || got[1] != "Bearer two" {
		t.Fatalf("authorization headers = %#v, want fresh headers per request", got)
	}
	if newConnections != 1 {
		t.Fatalf("connections = %d, want the refresh to reuse the connection", newConnections)
	}
}

// TestMcpRequestAuthRejectsOwnerChangeLikeRust covers the owner boundary: the
// session refuses a request whose provider now reports another owner, and the
// connection identity moves so the caller rebuilds it.
func TestMcpRequestAuthRejectsOwnerChangeLikeRust(t *testing.T) {
	auth := &rotatingAuth{owner: "owner-1", token: "one"}
	config, err := SetMcpRequestAuthProvider(&ServerConfig{URL: "https://example.com/mcp", Enabled: true}, NewMcpRequestAuthProvider(auth))
	if err != nil {
		t.Fatalf("SetMcpRequestAuthProvider() error = %v", err)
	}
	session := config.RequestAuthSession
	before := mcpConnectionCacheKey(config, false)
	headers := http.Header{}
	if err := session.AddAuthHeaders(headers); err != nil {
		t.Fatalf("AddAuthHeaders() error = %v", err)
	}
	auth.rotate("owner-2", "two")
	if !session.NeedsOwnerRefresh() {
		t.Fatal("NeedsOwnerRefresh() = false after the active owner changed")
	}
	if err := session.AddAuthHeaders(http.Header{}); err == nil || !strings.Contains(err.Error(), "owner changed") {
		t.Fatalf("AddAuthHeaders() error = %v, want the owner change", err)
	}
	if after := mcpConnectionCacheKey(config, false); after == before {
		t.Fatal("connection identity did not change with the owner")
	}
}

// TestMcpRequestAuthRejectsConflictingConfigurationLikeRust refuses another
// credential source on a provider-backed server.
func TestMcpRequestAuthRejectsConflictingConfigurationLikeRust(t *testing.T) {
	provider := NewMcpRequestAuthProvider(&rotatingAuth{owner: "owner-1"})
	for name, mutate := range map[string]func(*ServerConfig){
		"bearer":      func(c *ServerConfig) { c.BearerTokenEnvVar = "MCP_TOKEN" },
		"headers":     func(c *ServerConfig) { c.HTTPHeaders = map[string]string{"X-Key": "v"} },
		"env headers": func(c *ServerConfig) { c.EnvHTTPHeaders = map[string]string{"X-Key": "MCP_TOKEN"} },
		"helper":      func(c *ServerConfig) { c.HTTPHeadersHelper = "helper" },
		"oauth":       func(c *ServerConfig) { c.OAuthClientID = "client" },
	} {
		config := &ServerConfig{URL: "https://example.com/mcp", Enabled: true}
		mutate(config)
		if _, err := SetMcpRequestAuthProvider(config, provider); err == nil {
			t.Fatalf("%s: SetMcpRequestAuthProvider() accepted conflicting credentials", name)
		}
	}
	if _, err := SetMcpRequestAuthProvider(&ServerConfig{Command: "server", Enabled: true}, provider); err == nil {
		t.Fatal("SetMcpRequestAuthProvider() accepted a stdio server")
	}
}

// TestMcpRequestAuthDisablesRedirectsLikeRust keeps provider credentials on the
// origin that issued them.
func TestMcpRequestAuthDisablesRedirectsLikeRust(t *testing.T) {
	var redirected bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			redirected = true
			_, _ = io.WriteString(w, "final")
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer server.Close()
	config, err := SetMcpRequestAuthProvider(&ServerConfig{URL: server.URL, Enabled: true}, NewMcpRequestAuthProvider(&rotatingAuth{owner: "owner-1"}))
	if err != nil {
		t.Fatalf("SetMcpRequestAuthProvider() error = %v", err)
	}
	client := newMCPHTTPClient(config)
	response, err := client.client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound || redirected {
		t.Fatalf("redirect status = %d, followed = %t", response.StatusCode, redirected)
	}
}
