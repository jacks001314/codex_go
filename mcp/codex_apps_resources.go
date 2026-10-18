package mcp

import (
	"fmt"
	"strings"
)

// CodexAppsResourceListParams is one Codex Apps resource page request. The MIME
// filter is required and must be sent again with every continuation cursor
// (Rust #44548).
type CodexAppsResourceListParams struct {
	Cursor   *string `json:"cursor,omitempty"`
	MimeType string  `json:"mimeType"`
}

// MCPResourcePage is one page of resources plus the next cursor.
type MCPResourcePage struct {
	Resources  []MCPResource `json:"resources"`
	NextCursor *string       `json:"nextCursor,omitempty"`
}

// ListCodexAppsResources lists one MIME-filtered page through the host-owned
// Codex Apps registration. Extension servers using the same name are rejected
// without sending a request (Rust #44548).
func (s *MCPService) ListCodexAppsResources(params *CodexAppsResourceListParams) (*MCPResourcePage, error) {
	if s == nil {
		return nil, invalidMCPRequest("MCP service is unavailable")
	}
	mimeType := ""
	var cursor *string
	if params != nil {
		mimeType = strings.TrimSpace(params.MimeType)
		cursor = cloneStringPtr(params.Cursor)
	}
	if mimeType == "" {
		return nil, invalidMCPRequest("mimeType is required")
	}
	s.mu.Lock()
	status, hasStatus := s.servers[CodexAppsServerName]
	config, hasConfig := s.configs[CodexAppsServerName]
	s.mu.Unlock()
	if !hasStatus || !hasConfig || !status.hostOwnedApps {
		return nil, invalidMCPRequest("MCP server 'codex_apps' is not registered by the hosted runtime")
	}
	requestParams := map[string]any{"mimeType": mimeType}
	if cursor != nil {
		requestParams["cursor"] = *cursor
	}
	var response struct {
		Resources  []MCPResource `json:"resources"`
		NextCursor *string       `json:"nextCursor,omitempty"`
	}
	client := s.httpClientForServer(CodexAppsServerName, &config)
	if err := client.CallWithOptions(&httpClientCallOptions{ServerName: CodexAppsServerName}, "resources/list", requestParams, &response); err != nil {
		return nil, fmt.Errorf("resources/list failed for `%s`: %w", CodexAppsServerName, err)
	}
	return &MCPResourcePage{
		Resources:  append([]MCPResource(nil), response.Resources...),
		NextCursor: cloneStringPtr(response.NextCursor),
	}, nil
}
