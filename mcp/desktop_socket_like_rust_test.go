package mcp

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// withDesktopSocketPath points the Desktop route at a temporary socket for one
// test, mirroring how the production resolver uses the shared daemon directory.
func withDesktopSocketPath(t *testing.T, path string) {
	t.Helper()
	original := desktopSocketPathFor
	desktopSocketPathFor = func(uuid.UUID) (string, error) { return path, nil }
	t.Cleanup(func() { desktopSocketPathFor = original })
}

// shortDesktopSocketPath returns a socket path short enough for the AF_UNIX
// sun_path limit on every platform. Testing temp directories embed the test
// name, which can exceed that limit on Windows.
func shortDesktopSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "dsk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "desktop.sock")
}

// startDesktopSocketServer serves handler on a Unix socket and reports the
// listener so callers can replace it.
func startDesktopSocketServer(t *testing.T, path string, handler http.Handler) (*http.Server, net.Listener) {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen(unix, %s) error = %v", path, err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return server, listener
}

// TestDesktopMCPAdmissionLikeRust covers Rust #63497: only a local codex_app
// server with explicit HTTP authorization may address the Desktop socket.
func TestDesktopMCPAdmissionLikeRust(t *testing.T) {
	id := uuid.NewString()
	endpoint := "codex-desktop://" + id
	authorized := ServerConfig{URL: endpoint, HTTPHeaders: map[string]string{"Authorization": "Bearer desktop"}}
	if err := ValidateServerAuth("codex_app", &authorized); err != nil {
		t.Fatalf("authenticated Desktop server rejected: %v", err)
	}
	cases := map[string]struct {
		name   string
		config ServerConfig
	}{
		"helper alone cannot authorize": {
			name:   "codex_app",
			config: ServerConfig{URL: endpoint, HTTPHeadersHelper: "helper"},
		},
		"another server cannot use the socket": {
			name:   "other",
			config: authorized,
		},
		"remote environment cannot use the socket": {
			name:   "codex_app",
			config: ServerConfig{URL: endpoint, EnvironmentID: "sandbox-1", HTTPHeaders: map[string]string{"Authorization": "Bearer desktop"}},
		},
		"environment credentials do not authorize": {
			name:   "codex_app",
			config: ServerConfig{URL: endpoint, EnvHTTPHeaders: map[string]string{"Authorization": "MCP_AUTH"}, HTTPHeaders: map[string]string{"Authorization": "Bearer desktop"}},
		},
	}
	for label, tc := range cases {
		config := tc.config
		err := ValidateServerAuth(tc.name, &config)
		if err == nil || !strings.Contains(err.Error(), "Desktop MCP requires") {
			t.Fatalf("%s error = %v, want a Desktop MCP requirement", label, err)
		}
	}
	if err := ValidateServerAuth("codex_app", &ServerConfig{URL: "codex-desktop://not-a-uuid"}); err == nil {
		t.Fatal("invalid Desktop endpoint was accepted")
	}
}

// TestDesktopSocketMCPRoundTripLikeRust covers Rust #63497: the request reaches
// the Unix socket with the configured authorization, helper headers stay
// supplemental, proxy credentials are stripped, and redirects are not followed.
func TestDesktopSocketMCPRoundTripLikeRust(t *testing.T) {
	socketPath := shortDesktopSocketPath(t)
	withDesktopSocketPath(t, socketPath)
	type observed struct {
		authorization string
		proxyHeader   string
		extra         string
		path          string
	}
	seen := make(chan observed, 4)
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(writer http.ResponseWriter, request *http.Request) {
		seen <- observed{
			authorization: request.Header.Get("Authorization"),
			proxyHeader:   request.Header.Get("Proxy-Authorization"),
			extra:         request.Header.Get("X-Extra"),
			path:          request.URL.Path,
		}
		if request.URL.Path == "/mcp" && request.Header.Get("X-Redirect") != "" {
			writer.Header().Set("Location", "http://example.test/elsewhere")
			writer.WriteHeader(http.StatusFound)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	})
	_, _ = startDesktopSocketServer(t, socketPath, mux)

	config := &ServerConfig{
		URL:               "codex-desktop://" + uuid.NewString(),
		HTTPHeaders:       map[string]string{"Authorization": "Bearer desktop"},
		HTTPHeadersHelper: "ignored-for-test",
	}
	client := newMCPHTTPClient(config)
	if got := client.config.URL; got != desktopMCPWireURL {
		t.Fatalf("client URL = %q, want the socket wire URL %q", got, desktopMCPWireURL)
	}
	headerTransport, ok := client.client.Transport.(*mcpHeaderTransport)
	if !ok {
		t.Fatalf("transport = %T, want the configured-header transport", client.client.Transport)
	}
	helperTransport, ok := headerTransport.base.(*mcpHTTPHeadersHelperTransport)
	if !ok {
		t.Fatalf("base transport = %T, want the headers-helper transport", headerTransport.base)
	}
	helperTransport.run = func(context.Context, string, string) (http.Header, error) {
		return http.Header{"X-Extra": {"from-helper"}, "Proxy-Authorization": {"Basic test-proxy"}}, nil
	}

	request, err := http.NewRequest(http.MethodPost, client.config.URL, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Redirect", "")
	// The RPC path applies the configured headers before the helper and socket
	// transports see the request (http_client.go doRPC).
	applyMCPHTTPHeaders(request, client.config.HTTPHeaders, client.config.EnvHTTPHeaders)
	response, err := client.client.Do(request)
	if err != nil {
		t.Fatalf("Desktop socket request error = %v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	select {
	case got := <-seen:
		if got.authorization != "Bearer desktop" || got.extra != "from-helper" || got.proxyHeader != "" || got.path != "/mcp" {
			t.Fatalf("observed request = %#v", got)
		}
	default:
		t.Fatal("Desktop socket server received no request")
	}

	// A redirect response must be returned to the caller, never followed.
	redirectRequest, err := http.NewRequest(http.MethodGet, desktopMCPWireURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	redirectRequest.Header.Set("X-Redirect", "1")
	applyMCPHTTPHeaders(redirectRequest, client.config.HTTPHeaders, client.config.EnvHTTPHeaders)
	redirectResponse, err := client.client.Do(redirectRequest)
	if err != nil {
		t.Fatalf("Desktop redirect request error = %v", err)
	}
	defer redirectResponse.Body.Close()
	if redirectResponse.StatusCode != http.StatusFound {
		t.Fatalf("redirect status = %d, want the unfollowed 302", redirectResponse.StatusCode)
	}
}

// TestDesktopSocketMCPReconnectsWhenListenerReplacedLikeRust covers Rust #63497:
// pooled connections are discarded when the socket listener is replaced or the
// socket disappears, so requests never wait on a stale keep-alive connection.
func TestDesktopSocketMCPReconnectsWhenListenerReplacedLikeRust(t *testing.T) {
	socketPath := shortDesktopSocketPath(t)
	withDesktopSocketPath(t, socketPath)
	var mu sync.Mutex
	served := make([]string, 0, 2)
	first, firstListener := startDesktopSocketServer(t, socketPath, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		served = append(served, "first")
		mu.Unlock()
		_, _ = writer.Write([]byte("first"))
	}))
	config := &ServerConfig{URL: "codex-desktop://" + uuid.NewString(), HTTPHeaders: map[string]string{"Authorization": "Bearer desktop"}}
	client := newMCPHTTPClient(config)
	request := func() string {
		req, err := http.NewRequest(http.MethodGet, desktopMCPWireURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		applyMCPHTTPHeaders(req, client.config.HTTPHeaders, client.config.EnvHTTPHeaders)
		response, err := client.client.Do(req)
		if err != nil {
			t.Fatalf("Desktop socket request error = %v", err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return string(body)
	}
	if got := request(); got != "first" {
		t.Fatalf("first response = %q", got)
	}
	// Closing only the listener leaves the old keep-alive connection open, so a
	// pool that skipped the identity check would keep waiting on it.
	_ = firstListener.Close()
	_ = os.Remove(socketPath)
	_, _ = startDesktopSocketServer(t, socketPath, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		served = append(served, "second")
		mu.Unlock()
		_, _ = writer.Write([]byte("second"))
	}))
	if got := request(); got != "second" {
		t.Fatalf("response after listener replacement = %q, want the new listener", got)
	}
	mu.Lock()
	servedSnapshot := fmt.Sprint(served)
	mu.Unlock()
	if servedSnapshot != "[first second]" {
		t.Fatalf("served = %v, want both listeners", servedSnapshot)
	}
	// A socket that disappears is reported instead of reusing the pool.
	_ = first.Close()
	_ = os.Remove(socketPath)
	if _, err := client.client.Do(mustRequest(t, desktopMCPWireURL)); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("missing socket error = %v, want an unavailable socket", err)
	}
}

// mustRequest builds one request that every Desktop test can send.
func mustRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	return request
}
