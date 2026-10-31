package mcp

import (
	"context"
	"fmt"
	"strings"
)

// CodexAppsToolSnapshot is the exact Apps catalog returned by an awaited
// refresh of one published runtime (Rust #43031).
type CodexAppsToolSnapshot struct {
	// Tools is the raw installed inventory, including tools hidden or disabled
	// for the model. App-specific policy is applied by the caller.
	Tools []MCPToolInfo
	// ModelVisibleToolNames are the raw MCP tool names allowed by the same
	// runtime's generic MCP policy.
	ModelVisibleToolNames map[string]bool
}

// RefreshCodexAppsTools refreshes only the Codex Apps client's published
// catalog and returns that client's exact inventory together with the tool
// names eligible under the generic MCP policy. Other servers keep their
// connections and catalog revisions, so prepared calls for them stay valid.
func (s *MCPService) RefreshCodexAppsTools(ctx context.Context) (*CodexAppsToolSnapshot, error) {
	if s == nil {
		return nil, invalidMCPRequest("MCP service is unavailable")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	name := CodexAppsServerName
	config, ok := s.ServerConfigForServer(name)
	if !ok || !isRunnableMCPConfig(&config) {
		return nil, fmt.Errorf("unknown MCP server %q", name)
	}
	catalog, err := s.refreshClientToolCatalog(name, &config)
	if err != nil {
		return nil, err
	}
	tools := catalog.listedTools()
	modelVisibleToolNames := map[string]bool{}
	if _, ok := s.PermissionProfileForServer(name); ok {
		filtered := FilterMCPTools(tools, ToolFilterFromServerConfig(&config))
		for i := range filtered {
			if !mcpToolIsModelVisible(&filtered[i]) {
				continue
			}
			if toolName := strings.TrimSpace(filtered[i].Name); toolName != "" {
				modelVisibleToolNames[toolName] = true
			}
		}
	}
	return &CodexAppsToolSnapshot{Tools: tools, ModelVisibleToolNames: modelVisibleToolNames}, nil
}

// refreshClientToolCatalog lists one server through the exact client this
// service already uses for it, preserving connection reuse, and publishes the
// result on that client's catalog.
func (s *MCPService) refreshClientToolCatalog(name string, config *ServerConfig) (*clientToolCatalog, error) {
	catalog, httpTarget, stdioTarget, err := s.clientToolCatalogForServer(name, config)
	if err != nil {
		return nil, err
	}
	if catalog == nil {
		return nil, fmt.Errorf("MCP server %q has no tool catalog", name)
	}
	err = catalog.refresh(func() ([]MCPToolInfo, error) {
		if httpTarget != nil {
			if err := ValidateServerAuth(name, config); err != nil {
				return nil, err
			}
			tools, _, err := listMCPHTTPTools(httpTarget, &httpClientCallOptions{ServerName: name})
			return tools, err
		}
		tools, _, err := listMCPStdioTools(stdioTarget, &stdioCallOptions{ServerName: name})
		return tools, err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to refresh tools for MCP server %q: %w", name, err)
	}
	s.publishClientCatalogTools(name, catalog.listedTools())
	return catalog, nil
}

// clientToolCatalogForServer resolves the live connection this service would
// use for one server and returns the catalog that connection owns.
func (s *MCPService) clientToolCatalogForServer(name string, config *ServerConfig) (*clientToolCatalog, *httpClient, *stdioClient, error) {
	if err := ValidateExecutorDiscoveryCredentialPolicy(name, config); err != nil {
		return nil, nil, nil, err
	}
	if config != nil && strings.TrimSpace(config.URL) != "" {
		client := s.httpClientForServer(name, config)
		return client.toolCatalog(), client, nil, nil
	}
	if config != nil && strings.TrimSpace(config.Command) != "" {
		client := s.stdioClientForServer(name, config)
		return client.toolCatalog(), nil, client, nil
	}
	return nil, nil, nil, fmt.Errorf("MCP server %q has no transport", name)
}

// liveClientToolCatalog reports the catalog owned by the connection this
// service currently holds for a server, or nil when no connection is live.
func (s *MCPService) liveClientToolCatalog(name string) *clientToolCatalog {
	if s == nil {
		return nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached := s.httpClients[name]; cached != nil && cached.client != nil && !cached.client.isClosed() {
		return cached.client.toolCatalog()
	}
	if cached := s.stdioClients[name]; cached != nil && cached.client != nil && !cached.client.isClosed() {
		return cached.client.toolCatalog()
	}
	return nil
}

// publishClientCatalogTools mirrors a refreshed client catalog into the
// retained status so status reads keep reporting the published inventory.
func (s *MCPService) publishClientCatalogTools(name string, tools []MCPToolInfo) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	status, ok := s.servers[name]
	if !ok {
		return
	}
	status.Tools = cloneMCPToolInfos(tools)
	s.servers[name] = status
}
