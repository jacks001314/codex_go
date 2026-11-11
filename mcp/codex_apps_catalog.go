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
	for attempt := 0; attempt < 2; attempt++ {
		config, ok := s.ServerConfigForServer(name)
		if !ok || !isRunnableMCPConfig(&config) {
			return nil, fmt.Errorf("unknown MCP server %q", name)
		}
		generation, done := s.beginAppsToolRefresh()
		catalog, err := s.refreshClientToolCatalogUnpublished(name, &config)
		published := err == nil && catalog != nil && s.finishAppsToolRefresh(generation)
		if published {
			s.publishClientCatalogTools(name, catalog.listedTools())
		}
		done()
		if err != nil {
			return nil, err
		}
		if published {
			return appsToolSnapshot(catalog, &config, s), nil
		}
	}
	return nil, fmt.Errorf("MCP runtime changed during both Apps tool refresh attempts")
}

func appsToolSnapshot(catalog *clientToolCatalog, config *ServerConfig, service *MCPService) *CodexAppsToolSnapshot {
	tools := catalog.listedTools()
	modelVisibleToolNames := map[string]bool{}
	if _, ok := service.PermissionProfileForServer(CodexAppsServerName); ok {
		filtered := FilterMCPTools(tools, ToolFilterFromServerConfig(config))
		for i := range filtered {
			if !mcpToolIsModelVisible(&filtered[i]) {
				continue
			}
			if toolName := strings.TrimSpace(filtered[i].Name); toolName != "" {
				modelVisibleToolNames[toolName] = true
			}
		}
	}
	return &CodexAppsToolSnapshot{Tools: tools, ModelVisibleToolNames: modelVisibleToolNames}
}

// beginAppsToolRefresh registers one Apps catalog fetch against the runtime
// generation. The returned done function must run once the fetch publishes or
// gives up.
func (s *MCPService) beginAppsToolRefresh() (uint64, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.appsRefreshInFlight == 0 {
		s.appsRefreshIdle = make(chan struct{})
	}
	s.appsRefreshInFlight++
	generation := s.generation
	return generation, func() {
		s.mu.Lock()
		if s.appsRefreshInFlight > 0 {
			s.appsRefreshInFlight--
		}
		if s.appsRefreshInFlight == 0 && s.appsRefreshIdle != nil {
			close(s.appsRefreshIdle)
			s.appsRefreshIdle = nil
		}
		s.mu.Unlock()
	}
}

// finishAppsToolRefresh reports whether the captured generation is still
// current, so a superseded fetch cannot publish after configuration or auth
// changes (Rust #60639).
func (s *MCPService) finishAppsToolRefresh(generation uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.closed && s.generation == generation
}

// WaitForAppsToolRefresh blocks until every in-flight Apps catalog fetch has
// finished. Callers must not hold s.mu: refreshes fetch outside the runtime
// gate, and waiting outside it lets a superseded refresh retry.
func (s *MCPService) WaitForAppsToolRefresh(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		if s.appsRefreshInFlight == 0 {
			s.mu.Unlock()
			return nil
		}
		idle := s.appsRefreshIdle
		s.mu.Unlock()
		if idle == nil {
			continue
		}
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// refreshClientToolCatalog lists one server through the exact client this
// service already uses for it, preserving connection reuse, and publishes the
// result on that client's catalog.
func (s *MCPService) refreshClientToolCatalog(name string, config *ServerConfig) (*clientToolCatalog, error) {
	catalog, err := s.refreshClientToolCatalogUnpublished(name, config)
	if err != nil {
		return nil, err
	}
	s.publishClientCatalogTools(name, catalog.listedTools())
	return catalog, nil
}

// refreshClientToolCatalogUnpublished fetches one server's tools without
// publishing the retained status, so the Apps path can validate its generation
// first (Rust #60639).
func (s *MCPService) refreshClientToolCatalogUnpublished(name string, config *ServerConfig) (*clientToolCatalog, error) {
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
// liveHTTPClient reports the connection already authorized for an HTTP server.
// Discovery and calls pin to this exact transport, so a later refresh cannot
// transfer the search declaration to a different connection.
func (s *MCPService) liveHTTPClient(name string) *httpClient {
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
		return cached.client
	}
	return nil
}

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
