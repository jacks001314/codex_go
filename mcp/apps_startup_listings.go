package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
)

// appsStartupListings shares only overlapping successful fetches. A caller
// arriving after publication always performs a new listing on its own client.
// Scope keys must include the complete discovery identity, not just server name.
type appsStartupListings struct {
	mu      sync.Mutex
	pending map[string]*appsStartupListing
}

type appsStartupListing struct {
	done  chan struct{}
	tools []MCPToolInfo
	meta  json.RawMessage
	err   error
}

func (s *appsStartupListings) fetch(ctx context.Context, scope string, fetch func(context.Context) ([]MCPToolInfo, json.RawMessage, error)) ([]MCPToolInfo, json.RawMessage, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		s.mu.Lock()
		if s.pending == nil {
			s.pending = map[string]*appsStartupListing{}
		}
		active := s.pending[scope]
		if active == nil {
			active = &appsStartupListing{done: make(chan struct{})}
			s.pending[scope] = active
			s.mu.Unlock()
			tools, meta, err := fetch(ctx)
			if err == nil {
				err = ctx.Err()
			}
			s.mu.Lock()
			active.tools, active.meta, active.err = cloneAppsStartupTools(tools), append(json.RawMessage(nil), meta...), err
			delete(s.pending, scope)
			close(active.done)
			s.mu.Unlock()
			return tools, meta, err
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-active.done:
			// Deadline wins even if publication and cancellation become ready together.
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if active.err == nil {
				return cloneAppsStartupTools(active.tools), append(json.RawMessage(nil), active.meta...), nil
			}
			// A failed owner does not poison another independent MCP connection.
		}
	}
}

// listAppsStartupTools initializes this connection independently before joining
// an overlapping tools/list in the caller's validated discovery scope.
func listAppsStartupTools(ctx context.Context, client *httpClient, options *httpClientCallOptions, scope string, listings *appsStartupListings) ([]MCPToolInfo, json.RawMessage, error) {
	client.mu.Lock()
	client.applyCallOptions(options)
	var err error
	if !client.initialized {
		err = client.reinitialize(ctx)
	}
	client.mu.Unlock()
	if err != nil {
		return nil, nil, err
	}
	fetch := func(ctx context.Context) ([]MCPToolInfo, json.RawMessage, error) {
		return listMCPHTTPToolsWithContext(ctx, client, options)
	}
	if scope == "" || listings == nil || mcpServerOptsOutOfToolCatalogCache(client.serverCapabilitiesRaw()) {
		return fetch(ctx)
	}
	return listings.fetch(ctx, scope, fetch)
}

var sharedAppsStartupListings appsStartupListings

func (s *MCPService) appsStartupDiscoveryScope(name string, config *ServerConfig, roots []MCPRoot) string {
	if config == nil || config.appsDiscoveryOwner == "" {
		return ""
	}
	s.mu.Lock()
	httpKey := s.sharedHTTPClientKey
	if config.HTTPClient != nil {
		httpKey = mcpHTTPDoerIdentity(config.HTTPClient)
	}
	features := s.mcpHTTPClientCacheKey(config, s.openAIForm, s.serverAllowsUserVerificationLocked(name), httpKey)
	s.mu.Unlock()
	encoded, err := json.Marshal([]any{config.CodexHome, config.appsDiscoveryOwner, features, roots})
	if err != nil {
		return ""
	}
	// Only a digest of transport/header identity is retained by the coordinator.
	return fmt.Sprintf("%x", sha256.Sum256(encoded))
}

// Shared startup results are snapshots; clients may transform their own schema.
func cloneAppsStartupTools(tools []MCPToolInfo) []MCPToolInfo {
	out := cloneMCPToolInfos(tools)
	for i := range out {
		if out[i].InputSchema != nil {
			out[i].InputSchema, _ = cloneJSONValue(out[i].InputSchema).(map[string]any)
		}
		out[i].OutputSchema = cloneJSONValue(out[i].OutputSchema)
		out[i].Annotations = cloneJSONValue(out[i].Annotations)
		out[i].Meta = cloneJSONValue(out[i].Meta)
		if out[i].Icons != nil {
			out[i].Icons, _ = cloneJSONValue(out[i].Icons).([]any)
		}
	}
	return out
}
