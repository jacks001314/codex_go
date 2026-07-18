package appserver

import (
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
	if params != nil && params.ForceRefetch {
		p.service.Refresh()
	}
	threadID := ""
	if params != nil {
		threadID = strings.TrimSpace(params.ThreadID)
	}
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
	connectorTools := []mcp.ConnectorToolInfo{}
	ready := false
	if status != nil {
		for i := range status.Data {
			serverName := status.Data[i].Name
			if serverName == "" {
				serverName = status.Data[i].Server.Name
			}
			if !mcp.IsCodexAppsMCPServerName(serverName) {
				continue
			}
			ready = ready || status.Data[i].State == "" || status.Data[i].State == mcp.MCPServerReady
			connectorTools = append(connectorTools, mcp.ConnectorToolInfoFromMCPTools(serverName, status.Data[i].Tools)...)
		}
	}
	connectors := mcp.ConnectorAccessibleFromMCPTools(connectorTools)
	out := make([]apps.AppEntry, 0, len(connectors))
	for i := range connectors {
		out = append(out, appEntryFromMCPConnector(&connectors[i]))
	}
	return &apps.AppAccessibleListResponse{Apps: out, CodexAppsReady: ready}, nil
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
