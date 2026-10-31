package appserver

import (
	"context"
	"fmt"
	"strings"

	"codex_go/apps"
	"codex_go/mcp"
)

type mcpAccessibleAppsProvider struct {
	service *mcp.MCPService
}

func (p *mcpAccessibleAppsProvider) ListAccessibleApps(params *apps.AppAccessibleListParams) (*apps.AppAccessibleListResponse, error) {
	if p == nil || p.service == nil {
		return &apps.AppAccessibleListResponse{}, nil
	}
	threadID := ""
	if params != nil && params.ForceRefetch {
		// Rust #43031: a forced refresh publishes only the Apps client's
		// catalog, so other MCP servers keep their connections and the calls
		// prepared against them stay valid.
		snapshot, err := p.service.RefreshCodexAppsTools(context.Background())
		if err == nil {
			return &apps.AppAccessibleListResponse{
				Apps:           mcpAccessibleAppEntries(snapshot.Tools),
				CodexAppsReady: true,
			}, nil
		}
		// Rust #43039: a failed forced refresh keeps the last working catalog,
		// so report the retained Apps inventory and let app/installed decide
		// whether that catalog can be published.
		if retained, retainedErr := p.accessibleAppsFromStatus(threadID, true); retainedErr == nil {
			return retained, nil
		}
		return nil, err
	}
	if params != nil {
		threadID = strings.TrimSpace(params.ThreadID)
	}
	return p.accessibleAppsFromStatus(threadID, false)
}

// accessibleAppsFromStatus answers discovery from the retained status surface.
// When failOnEmpty is set a failed Apps server without a retained catalog is
// reported so a forced refresh does not publish an empty catalog.
func (p *mcpAccessibleAppsProvider) accessibleAppsFromStatus(threadID string, failOnEmpty bool) (*apps.AppAccessibleListResponse, error) {
	statusParams := &mcp.MCPListServerStatusParams{
		Detail: &mcp.MCPServerStatusDetail{
			Mode:         mcp.MCPServerStatusDetailToolsAndAuthOnly,
			IncludeTools: true,
		},
	}
	if threadID != "" {
		statusParams.ThreadID = &threadID
	}
	status, err := p.service.ListStatusChecked(statusParams)
	if err != nil {
		return nil, err
	}
	ready := false
	entries := make([]apps.AppEntry, 0)
	if status != nil {
		for i := range status.Data {
			serverName := status.Data[i].Name
			if serverName == "" {
				serverName = status.Data[i].Server.Name
			}
			if !mcp.IsCodexAppsMCPServerName(serverName) {
				continue
			}
			if failOnEmpty && status.Data[i].State == mcp.MCPServerFailed && len(status.Data[i].Tools) == 0 {
				return nil, fmt.Errorf("failed to refresh tools for MCP server %q", strings.TrimSpace(serverName))
			}
			ready = ready || status.Data[i].State == "" || status.Data[i].State == mcp.MCPServerReady
			entries = append(entries, mcpAccessibleAppEntries(status.Data[i].Tools)...)
		}
	}
	return &apps.AppAccessibleListResponse{Apps: entries, CodexAppsReady: ready}, nil
}

// mcpAccessibleAppEntries renders raw Codex Apps tools as installable app
// entries. App-specific enablement policy is applied by the caller.
func mcpAccessibleAppEntries(tools []mcp.MCPToolInfo) []apps.AppEntry {
	connectors := mcp.ConnectorAccessibleFromMCPTools(mcp.ConnectorToolInfoFromMCPTools(mcp.CodexAppsServerName, tools))
	out := make([]apps.AppEntry, 0, len(connectors))
	for i := range connectors {
		out = append(out, appEntryFromMCPConnector(&connectors[i]))
	}
	return out
}

func appEntryFromMCPConnector(connector *mcp.ConnectorAppInfo) apps.AppEntry {
	if connector == nil {
		return apps.AppEntry{}
	}
	name := strings.TrimSpace(connector.Name)
	if name == "" {
		name = strings.TrimSpace(connector.ID)
	}
	description := strings.TrimSpace(connector.Description)
	installURL := apps.ConnectorInstallURL(name, connector.ID)
	entry := apps.AppEntry{
		ID:                 strings.TrimSpace(connector.ID),
		Name:               name,
		InstallURL:         &installURL,
		IsAccessible:       true,
		IsEnabled:          connector.IsEnabled,
		Enabled:            connector.IsEnabled,
		EnabledExplicit:    true,
		PluginDisplayNames: append([]string(nil), connector.PluginDisplayNames...),
	}
	if description != "" {
		entry.Description = &description
	}
	if connector.Branding != nil && connector.Branding.IconURL != nil {
		entry.LogoURL = cloneStringPtrAppserver(connector.Branding.IconURL)
	}
	branding := mcpConnectorBrandingMap(connector)
	if len(branding) > 0 {
		entry.Branding = branding
	}
	return entry
}

func mcpConnectorBrandingMap(connector *mcp.ConnectorAppInfo) map[string]any {
	if connector == nil {
		return nil
	}
	branding := map[string]any{}
	if connector.Branding != nil {
		if connector.Branding.IconURL != nil && strings.TrimSpace(*connector.Branding.IconURL) != "" {
			branding["iconUrl"] = strings.TrimSpace(*connector.Branding.IconURL)
		}
		if connector.Branding.Color != nil && strings.TrimSpace(*connector.Branding.Color) != "" {
			branding["color"] = strings.TrimSpace(*connector.Branding.Color)
		}
	}
	if connector.Metadata != nil {
		if connector.Metadata.HomepageURL != nil && strings.TrimSpace(*connector.Metadata.HomepageURL) != "" {
			homepageURL := strings.TrimSpace(*connector.Metadata.HomepageURL)
			branding["website"] = homepageURL
			branding["homepageUrl"] = homepageURL
		}
		if connector.Metadata.DocsURL != nil && strings.TrimSpace(*connector.Metadata.DocsURL) != "" {
			branding["docsUrl"] = strings.TrimSpace(*connector.Metadata.DocsURL)
		}
	}
	if len(branding) == 0 {
		return nil
	}
	return branding
}
