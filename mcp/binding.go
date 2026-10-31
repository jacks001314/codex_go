package mcp

import (
	"fmt"
	"strings"
)

// Binding captures the MCP catalog revision that a model step observed.
type Binding struct {
	service    *MCPService
	generation uint64
	tools      map[string]map[string]bool
	// catalogs records the exact client catalog each server's tools came from.
	// A refreshed catalog advances only its own client's revision, so calls for
	// unrelated servers stay valid (Rust #43031).
	catalogs map[string]bindingCatalogRevision
	// cachedRevisions records the tool-list revision observed with a catalog
	// accepted under startup_readiness = "catalog". A later notification must
	// invalidate the binding even if the service generation did not change.
	cachedRevisions map[string]uint64
	// authorityPublished reports whether this runtime published per-server
	// permission authority (Rust #40728). When true, a server absent from
	// authority has no owner permissions and its calls are rejected.
	authorityPublished bool
	authority          map[string]bool
}

// bindingCatalogRevision identifies one client's catalog at one revision. The
// catalog pointer keeps clients with independent catalog histories distinct.
type bindingCatalogRevision struct {
	catalog  *clientToolCatalog
	revision uint64
}

func (s *MCPService) CaptureBinding(statuses []MCPServerStatus) *Binding {
	b := &Binding{service: s, generation: s.Generation(), tools: map[string]map[string]bool{}}
	if s != nil && s.HasPublishedPermissionAuthority() {
		b.authorityPublished = true
		b.authority = map[string]bool{}
	}
	for _, status := range statuses {
		server := status.effectiveName()
		if server == "" {
			continue
		}
		var set map[string]bool
		if config, ok := s.ServerConfigForServer(server); ok &&
			config.StartupReadiness.Effective() == MCPStartupReadinessCatalog {
			tools, usable := s.cachedCatalogStartupTools(server, &config, status)
			if !usable {
				continue
			}
			set = toolNameSet(tools)
			if b.cachedRevisions == nil {
				b.cachedRevisions = map[string]uint64{}
			}
			b.cachedRevisions[server] = s.cachedToolListRevision(server)
		} else {
			set = cachedTools(status)
			// Rust #41336 snapshots the cached catalog before waiting for server
			// startup. A non-ready server remains capturable when it already has a
			// published catalog; ordinary servers need at least one cached tool, while
			// the Apps server may legitimately publish an empty catalog.
			if status.State != "" && status.State != MCPServerReady &&
				len(set) == 0 && !IsCodexAppsMCPServerName(server) {
				continue
			}
		}
		if catalog := s.liveClientToolCatalog(server); catalog != nil {
			var revision uint64
			catalog.read(func(current uint64, tools []MCPToolInfo) {
				revision = current
				// Discovery may answer from the shared startup cache until this
				// client is refreshed; executable bindings always capture this
				// client's own catalog once it has published one.
				if current > 0 {
					set = toolNameSet(tools)
				}
			})
			if b.catalogs == nil {
				b.catalogs = map[string]bindingCatalogRevision{}
			}
			b.catalogs[server] = bindingCatalogRevision{catalog: catalog, revision: revision}
		}
		b.tools[server] = set
		if b.authorityPublished {
			if _, ok := s.PermissionProfileForServer(server); ok {
				b.authority[server] = true
			}
		}
	}
	return b
}

// cachedTools snapshots the tool names already published on a server status.
// It intentionally does not require the connection to be ready, matching
// Rust's AsyncManagedClient::cached_tools during binding capture (#41336).
func cachedTools(status MCPServerStatus) map[string]bool {
	tools := map[string]bool{}
	for _, tool := range status.Tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			tools[name] = true
		}
	}
	return tools
}
func (b *Binding) Generation() uint64 {
	if b == nil {
		return 0
	}
	return b.generation
}
func (b *Binding) CallTool(params *MCPToolCallParams) (*MCPToolCallResponse, error) {
	if b == nil || b.service == nil {
		return nil, invalidMCPRequest("MCP binding is unavailable")
	}
	if b.service.Generation() != b.generation {
		return nil, fmt.Errorf("%w: MCP tool catalog changed after this model step was prepared", ErrInvalidMCPRequest)
	}
	server := strings.TrimSpace(firstNonEmptyMCP(params.Server, params.ServerName))
	tool := strings.TrimSpace(firstNonEmptyMCP(params.Tool, params.ToolName))
	if !b.tools[server][tool] {
		return nil, fmt.Errorf("%w: MCP tool %q was not present in captured catalog for server %q", ErrInvalidMCPRequest, tool, server)
	}
	if revision, ok := b.cachedRevisions[server]; ok && b.service.cachedToolListRevision(server) != revision {
		return nil, fmt.Errorf("%w: MCP tool catalog changed after this model step was prepared", ErrInvalidMCPRequest)
	}
	if b.authorityPublished && !b.authority[server] {
		return nil, fmt.Errorf("%w: MCP server %q has no published permission authority", ErrInvalidMCPRequest, server)
	}
	var (
		response *MCPToolCallResponse
		err      error
	)
	if ref, ok := b.catalogs[server]; ok && ref.catalog != nil {
		// Hold the catalog's read lock for the call so a refresh publishes only
		// after calls prepared against the current revision finish (Rust #43031).
		ran, runErr := ref.catalog.runWithRevision(ref.revision, func() error {
			var callErr error
			response, callErr = b.service.CallTool(params)
			return callErr
		})
		if !ran {
			return nil, fmt.Errorf("%w: MCP tool catalog changed after this model step was prepared", ErrInvalidMCPRequest)
		}
		if runErr != nil {
			return nil, runErr
		}
	} else {
		response, err = b.service.CallTool(params)
		if err != nil {
			return nil, err
		}
	}
	if b.service.Generation() != b.generation {
		return nil, fmt.Errorf("%w: MCP tool catalog changed while the call was executing", ErrInvalidMCPRequest)
	}
	return response, nil
}

func toolNameSet(tools []MCPToolInfo) map[string]bool {
	set := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			set[name] = true
		}
	}
	return set
}
