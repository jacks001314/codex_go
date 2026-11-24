package mcp

// Rust parity: codex-rs/codex-mcp/src/rmcp_client.rs and
// http-client/src/route_aware_client_pool.rs (#63497). A codex-desktop://
// endpoint addresses the Desktop app's shared Unix socket instead of a TCP
// peer, so the socket supplies the peer while only the HTTP path and Host go
// on the wire.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/net/http/httpguts"
)

const (
	// desktopMCPServerName is the only MCP server the Desktop socket route
	// serves (Rust #63497).
	desktopMCPServerName = "codex_app"
	// desktopMCPWireURL is the only request URL a Desktop socket client sends:
	// the path and Host go on the wire and the socket supplies the peer.
	desktopMCPWireURL = "http://127.0.0.1:0/mcp"
	// desktopMCPPrefix selects the Desktop socket route.
	desktopMCPPrefix = "codex-desktop://"
)

// desktopSocketPathFor resolves the Desktop app's shared Unix socket. Tests
// replace it with a temporary socket so the transport stays testable on every
// platform Go can dial a Unix socket from.
var desktopSocketPathFor = func(id uuid.UUID) (string, error) {
	return desktopSocketPath(id)
}

// desktopMCPRoute identifies a codex-desktop:// URL. The reported error is
// non-nil when the endpoint is not a valid UUID, matching Rust's
// Uuid::parse_str failure.
func desktopMCPRoute(serverURL string) (uuid.UUID, bool, error) {
	value := strings.TrimSpace(serverURL)
	if !strings.HasPrefix(strings.ToLower(value), desktopMCPPrefix) {
		return uuid.Nil, false, nil
	}
	id, err := uuid.Parse(strings.TrimSpace(value[len(desktopMCPPrefix):]))
	if err != nil {
		return uuid.Nil, true, fmt.Errorf("invalid codex-desktop MCP endpoint %q: %w", value, err)
	}
	return id, true, nil
}

// desktopMCPHasExplicitAuthorization mirrors Rust's
// has_explicit_http_authorization: credentials resolved from the environment
// or an http_headers_helper never authorize a Desktop socket. Only a literal
// Authorization header qualifies.
func desktopMCPHasExplicitAuthorization(config *ServerConfig) bool {
	if config == nil {
		return false
	}
	if strings.TrimSpace(config.BearerTokenEnvVar) != "" || len(config.EnvHTTPHeaders) > 0 {
		return false
	}
	for name, value := range config.HTTPHeaders {
		if !strings.EqualFold(strings.TrimSpace(name), "authorization") {
			continue
		}
		if strings.TrimSpace(value) != "" && httpguts.ValidHeaderFieldValue(value) {
			return true
		}
	}
	return false
}

// ValidateDesktopMCPRoute mirrors Rust #63497's admission rule: only a local
// codex_app server with explicit HTTP authorization may address the Desktop
// socket. Every other configuration is refused before any request is made.
func ValidateDesktopMCPRoute(serverName string, config *ServerConfig) error {
	if config == nil {
		return nil
	}
	_, isDesktop, err := desktopMCPRoute(config.URL)
	if !isDesktop {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(serverName) != desktopMCPServerName ||
		!config.IsLocalEnvironment() ||
		!desktopMCPHasExplicitAuthorization(config) {
		return errors.New("Desktop MCP requires a local, authenticated codex_app server")
	}
	return nil
}

// prepareDesktopMCPConfig rewrites a valid Desktop endpoint to the wire URL and
// returns the socket path its transport must dial. It leaves the configuration
// untouched when the endpoint is not a Desktop URL.
func prepareDesktopMCPConfig(config *ServerConfig) (string, bool) {
	if config == nil {
		return "", false
	}
	id, isDesktop, err := desktopMCPRoute(config.URL)
	if !isDesktop {
		return "", false
	}
	if err != nil {
		return "", false
	}
	path, pathErr := desktopSocketPathFor(id)
	if pathErr != nil || strings.TrimSpace(path) == "" {
		// Without the socket the endpoint cannot be served; keeping the URL
		// lets the request fail with the endpoint error instead of silently
		// connecting somewhere else.
		return "", false
	}
	config.URL = desktopMCPWireURL
	return path, true
}

// desktopSocketTransport keeps one pooled transport per unchanged socket.
// The SSH forward can replace its listener while the old keep-alive connection
// stays open, so the socket's file identity is checked before every request
// and a replaced or missing socket discards the pool (Rust #63497).
type desktopSocketTransport struct {
	socketPath string

	mu        sync.Mutex
	transport *http.Transport
	identity  os.FileInfo
}

func newDesktopSocketTransport(socketPath string) *desktopSocketTransport {
	return &desktopSocketTransport{socketPath: socketPath}
}

func newDesktopSocketHTTPTransport(socketPath string) *http.Transport {
	dialer := &net.Dialer{}
	return &http.Transport{
		// The socket supplies the peer: no proxy can apply, and redirects are
		// handled by the client's CheckRedirect policy.
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
}

func (t *desktopSocketTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t == nil || request == nil {
		return nil, errors.New("nil Desktop MCP request")
	}
	info, statErr := os.Stat(t.socketPath)
	t.mu.Lock()
	replaced := t.transport == nil || statErr != nil || t.identity == nil || !os.SameFile(t.identity, info)
	if replaced {
		if t.transport != nil {
			t.transport.CloseIdleConnections()
		}
		t.transport = newDesktopSocketHTTPTransport(t.socketPath)
		t.identity = nil
		if statErr == nil {
			t.identity = info
		}
	}
	transport := t.transport
	t.mu.Unlock()
	if statErr != nil {
		return nil, fmt.Errorf("Desktop MCP socket %s is unavailable: %w", t.socketPath, statErr)
	}
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	// A proxy credential never applies to a socket request even when an
	// http_headers_helper supplied one (Rust #63497).
	cloned.Header.Del("Proxy-Authorization")
	return transport.RoundTrip(cloned)
}

func (t *desktopSocketTransport) CloseIdleConnections() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.transport != nil {
		t.transport.CloseIdleConnections()
	}
}
