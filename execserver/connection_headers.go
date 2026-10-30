package execserver

// Executor connection headers.
//
// Rust parity: codex-exec-server's RemoteEnvironmentOptions::into_transport_params
// (#47648). Headers supplied by the embedding host (for example an app-server
// environment registration's bearer token) are sent on the direct WebSocket
// upgrade and on reconnects. They must not be able to override the connection's
// own upgrade headers, a non-empty set requires a secure or loopback
// destination, and values are never echoed in diagnostics.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
)

// connectionControlledHeaders are the upgrade headers the WebSocket client owns;
// a caller-supplied header with one of these names is rejected.
var connectionControlledHeaders = map[string]bool{
	"connection":        true,
	"content-length":    true,
	"host":              true,
	"transfer-encoding": true,
	"upgrade":           true,
}

// HeadersAllowedForURL reports whether caller-supplied executor headers may be
// attached to this URL (Rust's wss:// or loopback destination rule). Hosts use
// it to reject a bearer token on an insecure remote connection before storing
// the environment registration.
func HeadersAllowedForURL(serverURL string) bool {
	return execServerHeadersAllowedForURL(serverURL)
}

// NormalizeHeaders validates and canonicalizes executor upgrade headers.
func NormalizeHeaders(serverURL string, headers http.Header) (http.Header, error) {
	return normalizeExecServerHeaders(serverURL, headers)
}

// normalizeExecServerHeaders validates caller-supplied upgrade headers for one
// exec-server URL and returns the canonical header set.
func normalizeExecServerHeaders(serverURL string, headers http.Header) (http.Header, error) {
	if len(headers) == 0 {
		return nil, nil
	}
	if !execServerHeadersAllowedForURL(serverURL) {
		return nil, errors.New("exec-server WebSocket headers require wss:// or a loopback destination")
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	normalized := make(http.Header, len(headers))
	for _, name := range names {
		canonical := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		if canonical == "" || !validExecServerHeaderName(canonical) {
			// The name travels in the error so a caller can fix it; it never
			// carries a value.
			return nil, fmt.Errorf("invalid exec-server WebSocket header name %q", name)
		}
		lower := strings.ToLower(canonical)
		if connectionControlledHeaders[lower] || strings.HasPrefix(lower, "sec-websocket-") {
			return nil, fmt.Errorf("exec-server WebSocket header %q is controlled by the connection", canonical)
		}
		if _, exists := normalized[canonical]; exists {
			return nil, fmt.Errorf("duplicate exec-server WebSocket header %q", canonical)
		}
		for _, value := range headers.Values(name) {
			if !validExecServerHeaderValue(value) {
				return nil, fmt.Errorf("invalid value for exec-server WebSocket header %q", canonical)
			}
			normalized.Add(canonical, value)
		}
	}
	return normalized, nil
}

// validExecServerHeaderName mirrors httpguts.ValidHeaderFieldName without
// pulling the dependency into this package's public surface.
func validExecServerHeaderName(name string) bool {
	for i := 0; i < len(name); i++ {
		switch c := name[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '!' || c == '#' || c == '$' || c == '%' || c == '&' || c == '\'' ||
			c == '*' || c == '+' || c == '-' || c == '.' || c == '^' || c == '_' ||
			c == '`' || c == '|' || c == '~':
		default:
			return false
		}
	}
	return name != ""
}

// validExecServerHeaderValue rejects values that cannot be sent on the wire.
func validExecServerHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if c := value[i]; c < 0x20 && c != '\t' || c == 0x7f {
			return false
		}
	}
	return true
}

// execServerHeadersAllowedForURL mirrors Rust's secure-transport rule: headers
// are only attached to wss:// or loopback destinations.
func execServerHeadersAllowedForURL(serverURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Scheme, "wss") {
		return true
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if address := net.ParseIP(host); address != nil {
		return address.IsLoopback()
	}
	return false
}

// RedactedExecServerHeaders renders header names with a fixed placeholder so
// diagnostics can identify which headers were supplied without echoing values
// (Rust marks the values sensitive).
func RedactedExecServerHeaders(headers http.Header) string {
	if len(headers) == 0 {
		return "{}"
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, fmt.Sprintf("%s=[redacted]", name))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// lookupHost resolves a header-bearing loopback WebSocket host. It is a
// variable so tests can pin mixed or non-loopback DNS answers.
var lookupHost = net.DefaultResolver.LookupHost

// isPlainWebSocketURL reports whether the destination uses the unencrypted ws
// scheme; only those connections take the loopback-direct path (Rust #42606).
func isPlainWebSocketURL(serverURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	return err == nil && strings.EqualFold(parsed.Scheme, "ws")
}

// loopbackWebSocketAddresses resolves a header-bearing ws:// destination and
// keeps only loopback addresses, mirroring Rust's `connect_loopback_direct`
// (#42606): trusted headers never travel to a peer that resolves elsewhere.
func loopbackWebSocketAddresses(ctx context.Context, serverURL string) ([]string, error) {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil {
		return nil, fmt.Errorf("invalid exec-server WebSocket URL: %w", err)
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, errors.New("exec-server WebSocket URL requires a host")
	}
	port := parsed.Port()
	if port == "" {
		port = "80"
	}
	var resolved []string
	if address := net.ParseIP(host); address != nil {
		resolved = []string{address.String()}
	} else {
		addresses, err := lookupHost(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve exec-server WebSocket host %q: %w", host, err)
		}
		resolved = addresses
	}
	loopback := make([]string, 0, len(resolved))
	for _, address := range resolved {
		parsedIP := net.ParseIP(address)
		if parsedIP == nil || !parsedIP.IsLoopback() {
			continue
		}
		loopback = append(loopback, net.JoinHostPort(parsedIP.String(), port))
	}
	if len(loopback) == 0 {
		return nil, errors.New("direct WebSocket connections must resolve to a loopback address")
	}
	return loopback, nil
}

// loopbackDirectWebSocketClient mirrors Rust's loopback-direct dial: the
// connection bypasses configured proxies and dials only resolved loopback
// addresses. A caller-supplied plain *http.Transport keeps its settings; other
// custom clients are used as-is after the loopback resolution check.
func loopbackDirectWebSocketClient(ctx context.Context, serverURL string, base *http.Client) (*http.Client, error) {
	addresses, err := loopbackWebSocketAddresses(ctx, serverURL)
	if err != nil {
		return nil, err
	}
	var transport *http.Transport
	if base != nil {
		if existing, ok := base.Transport.(*http.Transport); ok && existing != nil {
			transport = existing.Clone()
		} else if base.Transport != nil {
			return base, nil
		}
	}
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	transport.Proxy = nil
	dialer := &net.Dialer{}
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		var lastErr error
		for _, address := range addresses {
			conn, err := dialer.DialContext(ctx, network, address)
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr == nil {
			lastErr = errors.New("no loopback address to dial")
		}
		return nil, lastErr
	}
	client := &http.Client{Transport: transport}
	if base != nil {
		client.Timeout = base.Timeout
		client.CheckRedirect = base.CheckRedirect
		client.Jar = base.Jar
	}
	return client, nil
}
