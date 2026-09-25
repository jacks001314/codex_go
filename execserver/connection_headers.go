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
