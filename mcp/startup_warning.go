package mcp

import (
	"errors"
	"log/slog"
	"strings"
)

// isHTTPMCPConfig reports whether the server uses the Streamable HTTP
// transport. Rust #62906 scopes the credential-free startup warning to that
// transport; stdio servers keep the full startup error on stderr.
func isHTTPMCPConfig(config *ServerConfig) bool {
	return config != nil && strings.TrimSpace(config.URL) != ""
}

// warnMCPStartupUnavailable reports a Streamable HTTP startup problem once per
// connection with the server name and a short reason. Rust #62906 keeps the raw
// transport error out of this warning because it can echo the endpoint URL,
// credentials included. The gate lives on the HTTP connection, so a retry that
// reuses the connection does not repeat the warning.
func (s *MCPService) warnMCPStartupUnavailable(name, reason string) {
	client := s.startupWarningClient(name)
	if client == nil || client.startupWarningEmitted.Swap(true) {
		return
	}
	slog.Warn("MCP server unavailable", "target", "codex_mcp_startup", "server_name", name, "reason", reason)
}

// startupWarningClient returns the cached HTTP connection for a server without
// touching the connection's own mutex. A startup in progress holds that mutex
// for its whole network exchange, and the warning gate is a plain atomic on the
// connection, so the service lock is enough to identify it.
func (s *MCPService) startupWarningClient(name string) *httpClient {
	if s == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached := s.httpClients[name]; cached != nil {
		return cached.client
	}
	return nil
}

// mcpStartupUnavailableReason maps a startup failure to the short reason Rust
// #62906 reports for Streamable HTTP servers. Authentication is classified
// first so a 401 is described as a sign-in problem, then HTTP status errors and
// the common transport failures from the error text.
func mcpStartupUnavailableReason(err error, authenticationRequired bool) string {
	if authenticationRequired {
		return "authentication required; sign in to the MCP server"
	}
	var status *mcpHTTPStatusError
	if errors.As(err, &status) {
		switch status.StatusCode {
		case 403:
			return "HTTP 403 Forbidden"
		case 404:
			return "HTTP 404 Not Found"
		default:
			return "HTTP response error"
		}
	}
	lower := strings.ToLower(mcpStartupErrorMessage(err))
	switch {
	case strings.Contains(lower, "certificate"):
		return "TLS certificate error"
	case strings.Contains(lower, "dns error"),
		strings.Contains(lower, "no such host"),
		strings.Contains(lower, "server misbehaving"):
		return "DNS lookup failed"
	case strings.Contains(lower, "connection refused"):
		return "connection refused"
	case strings.Contains(lower, "http/request failed"):
		return "HTTP request failed"
	case strings.Contains(lower, "timed out"), strings.Contains(lower, "timeout"):
		return "startup timed out"
	default:
		return "initialization failed"
	}
}
