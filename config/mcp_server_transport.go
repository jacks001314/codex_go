package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// validateMCPServerTransportFields enforces Rust's
// `TryFrom<RawMcpServerConfig> for McpServerConfig` transport rules
// (config/src/mcp_types.rs): a table that names a command is a stdio server and
// must not carry streamable-HTTP fields, a table that names a URL is an HTTP
// server and must not carry stdio fields, and a table with neither is invalid.
// Rust fails config load on these, so Go must not silently pick one transport
// and ignore the conflicting fields.
func validateMCPServerTransportFields(value any) error {
	servers, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		table, ok := servers[name].(map[string]any)
		if !ok {
			continue
		}
		if err := validateMCPServerTransportTable(table); err != nil {
			return fmt.Errorf("mcp_servers.%s: %w", name, err)
		}
	}
	return nil
}

// stdioForbiddenFields and httpForbiddenFields mirror Rust's `throw_if_set`
// calls for each transport, in the order Rust evaluates them.
var (
	stdioForbiddenFields = []string{
		"url", "bearer_token_env_var", "bearer_token", "http_headers_helper",
		"http_headers", "env_http_headers", "oauth", "oauth_resource", "auth",
	}
	httpForbiddenFields = []string{"args", "env", "env_vars", "cwd", "bearer_token"}
)

func validateMCPServerTransportTable(table map[string]any) error {
	if mcpTransportFieldSet(table, "command") {
		for _, field := range stdioForbiddenFields {
			if mcpTransportFieldSet(table, field) {
				return fmt.Errorf("%s is not supported for stdio", field)
			}
		}
		return nil
	}
	if !mcpTransportFieldSet(table, "url") {
		return errors.New("invalid transport")
	}
	for _, field := range httpForbiddenFields {
		if mcpTransportFieldSet(table, field) {
			return fmt.Errorf("%s is not supported for streamable_http", field)
		}
	}
	if mcpTransportFieldSet(table, "http_headers_helper") {
		helper, _ := table["http_headers_helper"].(string)
		if strings.TrimSpace(helper) == "" {
			return errors.New("http_headers_helper must not be empty")
		}
		environment := strings.TrimSpace(mcpTransportString(table["environment_id"]))
		if environment != "" && environment != "local" {
			return errors.New("http_headers_helper is only supported for local MCP servers")
		}
	}
	return nil
}

// mcpTransportFieldSet reports whether the key is present with a non-null value,
// matching serde's `Option<T>` (an explicit null is `None`, an empty string or
// list is `Some`).
func mcpTransportFieldSet(table map[string]any, key string) bool {
	value, ok := table[key]
	return ok && value != nil
}

func mcpTransportString(value any) string {
	text, _ := value.(string)
	return text
}
