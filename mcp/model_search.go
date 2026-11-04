package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const SearchConnector = "connector_openai_search_service"

const modelSearchDiscoveryTimeout = 5 * time.Second

// SearchDiscoveryKind is the outcome of resolving the model's Search connector
// profile against its already authorized Apps connection.
type SearchDiscoveryKind string

const (
	SearchDiscoveryReady       SearchDiscoveryKind = "ready"
	SearchDiscoveryCritical    SearchDiscoveryKind = "critical"
	SearchDiscoveryUnavailable SearchDiscoveryKind = "unavailable"
)

// SearchDiscovery carries either the projected binding or a bounded reason for
// withholding a native fallback.
type SearchDiscovery struct {
	Kind    SearchDiscoveryKind
	Binding *Binding
	Reason  string
}

// IsModelSearchMCPTool reports whether a raw Codex Apps declaration is the
// connector-owned web_run tool.
func IsModelSearchMCPTool(tool *MCPToolInfo) bool {
	if tool == nil {
		return false
	}
	return isModelSearchIdentity(tool.Name, mcpToolConnectorID(tool), SearchConnector)
}

// IsModelSearchTool reports whether a runtime declaration is the model Search
// tool from the host-owned Apps connection.
func IsModelSearchTool(tool *RuntimeToolInfo) bool {
	if tool == nil || !tool.HostOwnedApps || !IsCodexAppsMCPServerName(tool.ServerName) {
		return false
	}
	return isModelSearchIdentity(tool.Tool.Name, tool.ConnectorID, SearchConnector)
}

func isModelSearchIdentity(name string, connectorID string, wantConnector string) bool {
	if strings.TrimSpace(connectorID) != wantConnector {
		return false
	}
	name = strings.TrimSpace(name)
	index := strings.LastIndex(name, ".")
	return index >= 0 && index < len(name)-1 && name[index+1:] == "web_run"
}

func mcpToolConnectorID(tool *MCPToolInfo) string {
	if tool == nil {
		return ""
	}
	connector := ConnectorToolInfoFromMCPTool(CodexAppsServerName, tool)
	if connector == nil {
		return ""
	}
	return strings.TrimSpace(connector.ConnectorID)
}

// SearchTool returns the projected declaration for the current step.
func (b *Binding) SearchTool() (RuntimeToolInfo, bool) {
	if b == nil || b.searchTool == nil {
		return RuntimeToolInfo{}, false
	}
	return cloneRuntimeToolInfo(b.searchTool), true
}

// SearchInvocationContext returns the connector-owned opaque pin captured with
// the projected declaration.
func (b *Binding) SearchInvocationContext() (any, bool) {
	if b == nil || b.searchInvocationContext == nil {
		return nil, false
	}
	return cloneJSONValue(b.searchInvocationContext), true
}

// ModelSearchInvocationContext returns the opaque connector pin carried by a
// projected Search declaration. Keeping this lookup in mcp prevents a caller
// from treating arbitrary Apps metadata as a search pin.
func ModelSearchInvocationContext(tool *RuntimeToolInfo) (any, bool) {
	if !IsModelSearchTool(tool) {
		return nil, false
	}
	meta, _ := tool.Meta.(map[string]any)
	appsMeta := connectorMetaMap(meta[MCPToolCodexAppsMetaKey])
	context, ok := appsMeta["invocation_context"]
	if !ok || context == nil {
		return nil, false
	}
	return cloneJSONValue(context), true
}

// DiscoverModelSearch lists the already authorized host-owned Apps connection
// with the current model's search profile. It never creates a new transport or
// treats an empty catalog as a successful discovery.
func (b *Binding) DiscoverModelSearch(ctx context.Context, turnMetadata any) SearchDiscovery {
	if b == nil || b.service == nil || b.service.Generation() != b.generation {
		return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
	}
	server := CodexAppsServerName
	if !b.hostOwnedApps[server] {
		return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
	}
	var original *MCPToolInfo
	for i := range b.toolInfos[server] {
		if IsModelSearchMCPTool(&b.toolInfos[server][i]) {
			original = &b.toolInfos[server][i]
			break
		}
	}
	if original == nil || !b.tools[server][strings.TrimSpace(original.Name)] {
		return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
	}
	client := b.service.liveHTTPClient(server)
	if client == nil {
		return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
	}
	if ref, ok := b.catalogs[server]; ok && ref.catalog != client.toolCatalog() {
		return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
	}
	metadata := cloneJSONValue(turnMetadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	requestCtx, cancel := context.WithTimeout(ctx, modelSearchDiscoveryTimeout)
	defer cancel()
	tools, _, err := listMCPHTTPToolsWithContext(requestCtx, client, &httpClientCallOptions{
		ServerName: server,
		MetaForSession: func(json.RawMessage) (any, error) {
			return map[string]any{"x-codex-turn-metadata": cloneJSONValue(metadata)}, nil
		},
	})
	if err != nil {
		return modelSearchDiscoveryFailure(err)
	}
	var listed *MCPToolInfo
	for i := range tools {
		tool := &tools[i]
		if mcpToolConnectorID(tool) == SearchConnector && tool.Name == original.Name {
			listed = tool
			break
		}
	}
	if listed == nil {
		return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
	}
	projected, invocationContext, ok := projectModelSearchTool(*listed)
	if !ok {
		return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
	}
	return SearchDiscovery{Kind: SearchDiscoveryReady, Binding: b.withSearchProjection(server, projected, invocationContext)}
}

func projectModelSearchTool(tool MCPToolInfo) (MCPToolInfo, any, bool) {
	connector := ConnectorToolInfoFromMCPTool(CodexAppsServerName, &tool)
	if connector == nil || strings.TrimSpace(connector.ConnectorID) != SearchConnector {
		return MCPToolInfo{}, nil, false
	}
	meta, _ := tool.Meta.(map[string]any)
	appsMeta := connectorMetaMap(meta[MCPToolCodexAppsMetaKey])
	invocationContext, ok := appsMeta["invocation_context"].(map[string]any)
	if !ok || invocationContext == nil {
		return MCPToolInfo{}, nil, false
	}
	if visible := ToolModelVisible(&tool); visible != nil && !*visible {
		return MCPToolInfo{}, nil, false
	}
	return tool, cloneAnyMap(invocationContext), true
}

func modelSearchDiscoveryFailure(err error) SearchDiscovery {
	if err == nil {
		return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return SearchDiscovery{Kind: SearchDiscoveryCritical, Reason: "discovery_timeout"}
	}
	code := mcpRequestErrorCode(err)
	if code == "transport_closed" || code == "request_timeout" || isRetryableMCPStreamableHTTPError(err) {
		return SearchDiscovery{Kind: SearchDiscoveryCritical, Reason: "discovery_error"}
	}
	var remote *MCPRemoteError
	if errors.As(err, &remote) && remote.Code == mcpJSONRPCInternalErrorCode {
		return SearchDiscovery{Kind: SearchDiscoveryCritical, Reason: "discovery_error"}
	}
	return SearchDiscovery{Kind: SearchDiscoveryUnavailable}
}

func (b *Binding) withSearchProjection(server string, tool MCPToolInfo, invocationContext any) *Binding {
	clone := *b
	clone.tools = make(map[string]map[string]bool, len(b.tools))
	for name, set := range b.tools {
		copied := make(map[string]bool, len(set))
		for toolName, visible := range set {
			copied[toolName] = visible
		}
		clone.tools[name] = copied
	}
	clone.toolInfos = make(map[string][]MCPToolInfo, len(b.toolInfos))
	for name, tools := range b.toolInfos {
		clone.toolInfos[name] = cloneMCPToolInfos(tools)
	}
	clone.hostOwnedApps = make(map[string]bool, len(b.hostOwnedApps))
	for name, hostOwned := range b.hostOwnedApps {
		clone.hostOwnedApps[name] = hostOwned
	}
	for i := range clone.toolInfos[server] {
		if IsModelSearchMCPTool(&clone.toolInfos[server][i]) {
			clone.toolInfos[server][i] = tool
			break
		}
	}
	runtimeTool := runtimeSearchToolInfo(tool)
	clone.searchTool = &runtimeTool
	clone.searchInvocationContext = cloneJSONValue(invocationContext)
	return &clone
}

func runtimeSearchToolInfo(tool MCPToolInfo) RuntimeToolInfo {
	info := RuntimeToolInfo{
		ServerName:    RuntimeCodexAppsMCPServerName,
		HostOwnedApps: true,
		Meta:          cloneJSONValue(tool.Meta),
		Tool: RuntimeTool{
			Name:         strings.TrimSpace(tool.Name),
			Title:        strings.TrimSpace(tool.Title),
			Description:  strings.TrimSpace(tool.Description),
			InputSchema:  cloneAnyMap(tool.InputSchema),
			Annotations:  RuntimeToolAnnotationsFromMCP(tool.Annotations),
			ModelVisible: ToolModelVisible(&tool),
		},
	}
	if connector := ConnectorToolInfoFromMCPTool(CodexAppsServerName, &tool); connector != nil {
		info.ConnectorID = strings.TrimSpace(connector.ConnectorID)
		info.ConnectorName = strings.TrimSpace(connector.ConnectorName)
		info.ConnectorDescription = strings.TrimSpace(connector.ConnectorDescription)
		info.NamespaceDescription = firstNonEmpty(connector.NamespaceDescription, connector.ConnectorDescription)
		info.PluginDisplayNames = append([]string(nil), connector.PluginDisplayNames...)
	}
	return info
}
