package mcp

import (
	"sort"
	"strings"
	"sync"
	"time"
)

const ConnectorCodexAppsMCPServerName = "codex-apps"

type ConnectorAppBranding struct {
	IconURL *string `json:"iconUrl,omitempty"`
	Color   *string `json:"color,omitempty"`
}

type ConnectorAppMetadata struct {
	HomepageURL *string `json:"homepageUrl,omitempty"`
	DocsURL     *string `json:"docsUrl,omitempty"`
}

type ConnectorAppInfo struct {
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	Description        string                `json:"description,omitempty"`
	Branding           *ConnectorAppBranding `json:"branding,omitempty"`
	Metadata           *ConnectorAppMetadata `json:"metadata,omitempty"`
	IsAccessible       bool                  `json:"isAccessible"`
	IsEnabled          bool                  `json:"isEnabled"`
	PluginDisplayNames []string              `json:"pluginDisplayNames,omitempty"`
}

type ConnectorAccessibleStatus struct {
	Connectors     []ConnectorAppInfo `json:"connectors"`
	CodexAppsReady bool               `json:"codexAppsReady"`
}

type ConnectorToolInfo struct {
	ServerName           string
	ConnectorID          string
	ConnectorName        string
	ConnectorDescription string
	NamespaceDescription string
	IsEnabled            *bool
	IconURL              string
	Color                string
	HomepageURL          string
	DocsURL              string
	PluginDisplayNames   []string
	Meta                 map[string]any
}

type ConnectorAppsConfig struct {
	DefaultEnabled bool
	Apps           map[string]ConnectorAppConfig
}

type ConnectorAppConfig struct {
	Enabled *bool
}

type ConnectorPluginProvenance struct {
	byConnector map[string][]string
}

func NewConnectorPluginProvenance() *ConnectorPluginProvenance {
	return &ConnectorPluginProvenance{byConnector: map[string][]string{}}
}

func (p *ConnectorPluginProvenance) Add(connectorID string, displayName string) {
	if p.byConnector == nil {
		p.byConnector = map[string][]string{}
	}
	connectorID = strings.TrimSpace(connectorID)
	displayName = strings.TrimSpace(displayName)
	if connectorID == "" || displayName == "" {
		return
	}
	for _, existing := range p.byConnector[connectorID] {
		if existing == displayName {
			return
		}
	}
	p.byConnector[connectorID] = append(p.byConnector[connectorID], displayName)
	sort.Strings(p.byConnector[connectorID])
}

func (p *ConnectorPluginProvenance) Names(connectorID string) []string {
	if p == nil {
		return nil
	}
	connectorID = strings.TrimSpace(connectorID)
	return append([]string(nil), p.byConnector[connectorID]...)
}

func ConnectorAccessibleFromMCPTools(tools []ConnectorToolInfo) []ConnectorAppInfo {
	byID := map[string]*ConnectorAppInfo{}
	for i := range tools {
		tool := tools[i]
		tool.ConnectorID = strings.TrimSpace(tool.ConnectorID)
		if !IsCodexAppsMCPServerName(tool.ServerName) || tool.ConnectorID == "" {
			continue
		}
		if isSyntheticLink(tool.Meta) {
			continue
		}
		connector := byID[tool.ConnectorID]
		if connector == nil {
			connector = &ConnectorAppInfo{
				ID:           tool.ConnectorID,
				Name:         tool.ConnectorID,
				IsAccessible: true,
				IsEnabled:    true,
			}
			byID[tool.ConnectorID] = connector
		}
		mergeConnectorToolMetadata(connector, &tool)
		connector.PluginDisplayNames = mergeStrings(connector.PluginDisplayNames, tool.PluginDisplayNames)
	}
	out := make([]ConnectorAppInfo, 0, len(byID))
	for _, connector := range byID {
		cloned := cloneConnectorAppInfo(*connector)
		sort.Strings(cloned.PluginDisplayNames)
		out = append(out, cloned)
	}
	sort.SliceStable(out, func(i int, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func IsCodexAppsMCPServerName(name string) bool {
	name = strings.TrimSpace(name)
	return name == ConnectorCodexAppsMCPServerName || name == CodexAppsServerName || name == RuntimeCodexAppsMCPServerName
}

func ConnectorToolInfoFromMCPTools(serverName string, tools []MCPToolInfo) []ConnectorToolInfo {
	out := make([]ConnectorToolInfo, 0, len(tools))
	for i := range tools {
		if tool := ConnectorToolInfoFromMCPTool(serverName, &tools[i]); tool != nil {
			out = append(out, *tool)
		}
	}
	return out
}

func ConnectorToolInfoFromMCPTool(serverName string, tool *MCPToolInfo) *ConnectorToolInfo {
	if tool == nil || !IsCodexAppsMCPServerName(serverName) {
		return nil
	}
	annotationMeta := connectorMetaMap(tool.Annotations)
	toolMeta := connectorMetaMap(tool.Meta)
	annotationNested := nestedConnectorMetaMap(annotationMeta)
	toolNested := nestedConnectorMetaMap(toolMeta)
	meta := mergeConnectorMetaMaps(annotationMeta, toolMeta)
	branding := firstConnectorMetaMap(annotationNested, annotationMeta, "branding", "connector_branding", "connectorBranding")
	if branding == nil {
		branding = firstConnectorMetaMap(toolNested, toolMeta, "branding", "connector_branding", "connectorBranding")
	}
	metadata := firstConnectorMetaMap(toolNested, toolMeta, "metadata", "connector_metadata", "connectorMetadata")
	if metadata == nil {
		metadata = firstConnectorMetaMap(annotationNested, annotationMeta, "metadata", "connector_metadata", "connectorMetadata")
	}
	connectorID := firstNonEmpty(
		connectorMetaString(toolNested, "connector_id", "connectorId", "connectorID"),
		connectorMetaString(toolMeta, "connector_id", "connectorId", "connectorID"),
		connectorMetaString(annotationNested, "connector_id", "connectorId", "connectorID"),
		connectorMetaString(annotationMeta, "connector_id", "connectorId", "connectorID"),
	)
	if strings.TrimSpace(connectorID) == "" {
		return nil
	}
	return &ConnectorToolInfo{
		ServerName:           serverName,
		ConnectorID:          connectorID,
		ConnectorName:        firstNonEmpty(connectorMetaString(toolNested, "connector_name", "connectorName", "name"), connectorMetaString(toolMeta, "connector_name", "connectorName"), connectorMetaString(annotationNested, "connector_name", "connectorName", "name"), connectorMetaString(annotationMeta, "connector_name", "connectorName")),
		ConnectorDescription: firstNonEmpty(connectorMetaString(toolNested, "connector_description", "connectorDescription", "description"), connectorMetaString(toolMeta, "connector_description", "connectorDescription"), connectorMetaString(annotationNested, "connector_description", "connectorDescription", "description"), connectorMetaString(annotationMeta, "connector_description", "connectorDescription")),
		NamespaceDescription: firstNonEmpty(connectorMetaString(toolNested, "namespace_description", "namespaceDescription"), connectorMetaString(toolMeta, "namespace_description", "namespaceDescription"), connectorMetaString(annotationNested, "namespace_description", "namespaceDescription"), connectorMetaString(annotationMeta, "namespace_description", "namespaceDescription")),
		IsEnabled:            firstConnectorMetaBool(toolNested, toolMeta, "is_enabled", "isEnabled", "enabled", "connector_enabled", "connectorEnabled"),
		IconURL: firstNonEmpty(
			connectorMetaString(annotationNested, "connector_icon_url", "connectorIconUrl", "connector_logo_url", "connectorLogoUrl", "icon_url", "iconUrl", "logo_url", "logoUrl"),
			connectorMetaString(branding, "icon_url", "iconUrl", "logo_url", "logoUrl", "url"),
			connectorMetaString(toolNested, "connector_icon_url", "connectorIconUrl", "connector_logo_url", "connectorLogoUrl", "icon_url", "iconUrl", "logo_url", "logoUrl"),
			connectorMetaString(toolMeta, "connector_icon_url", "connectorIconUrl", "connector_logo_url", "connectorLogoUrl", "icon_url", "iconUrl", "logo_url", "logoUrl"),
		),
		Color: firstNonEmpty(
			connectorMetaString(annotationNested, "connector_color", "connectorColor", "color"),
			connectorMetaString(branding, "color"),
			connectorMetaString(toolNested, "connector_color", "connectorColor", "color"),
			connectorMetaString(toolMeta, "connector_color", "connectorColor", "color"),
		),
		HomepageURL: firstNonEmpty(
			connectorMetaString(toolNested, "homepage_url", "homepageUrl", "website_url", "websiteUrl"),
			connectorMetaString(metadata, "homepage_url", "homepageUrl", "website_url", "websiteUrl"),
			connectorMetaString(annotationNested, "homepage_url", "homepageUrl", "website_url", "websiteUrl"),
			connectorMetaString(annotationMeta, "homepage_url", "homepageUrl", "website_url", "websiteUrl"),
		),
		DocsURL: firstNonEmpty(
			connectorMetaString(toolNested, "docs_url", "docsUrl", "documentation_url", "documentationUrl"),
			connectorMetaString(metadata, "docs_url", "docsUrl", "documentation_url", "documentationUrl"),
			connectorMetaString(annotationNested, "docs_url", "docsUrl", "documentation_url", "documentationUrl"),
			connectorMetaString(annotationMeta, "docs_url", "docsUrl", "documentation_url", "documentationUrl"),
		),
		PluginDisplayNames: mergeStrings(mergeStrings(connectorMetaStrings(annotationNested, "plugin_display_names", "pluginDisplayNames"), connectorMetaStrings(annotationMeta, "plugin_display_names", "pluginDisplayNames")), mergeStrings(connectorMetaStrings(toolNested, "plugin_display_names", "pluginDisplayNames"), connectorMetaStrings(toolMeta, "plugin_display_names", "pluginDisplayNames"))),
		Meta:               meta,
	}
}

func mergeConnectorToolMetadata(connector *ConnectorAppInfo, tool *ConnectorToolInfo) {
	if connector == nil || tool == nil {
		return
	}
	if strings.TrimSpace(connector.Name) == "" || connector.Name == connector.ID {
		if name := firstNonEmpty(tool.ConnectorName, tool.ConnectorID); name != "" {
			connector.Name = name
		}
	}
	if strings.TrimSpace(connector.Description) == "" {
		connector.Description = firstNonEmpty(tool.ConnectorDescription, tool.NamespaceDescription)
	}
	if tool.IsEnabled != nil && !*tool.IsEnabled {
		connector.IsEnabled = false
	}
	if iconURL := strings.TrimSpace(tool.IconURL); iconURL != "" {
		if connector.Branding == nil {
			connector.Branding = &ConnectorAppBranding{}
		}
		if connector.Branding.IconURL == nil || strings.TrimSpace(*connector.Branding.IconURL) == "" {
			connector.Branding.IconURL = &iconURL
		}
	}
	if color := strings.TrimSpace(tool.Color); color != "" {
		if connector.Branding == nil {
			connector.Branding = &ConnectorAppBranding{}
		}
		if connector.Branding.Color == nil || strings.TrimSpace(*connector.Branding.Color) == "" {
			connector.Branding.Color = &color
		}
	}
	if homepageURL := strings.TrimSpace(tool.HomepageURL); homepageURL != "" {
		if connector.Metadata == nil {
			connector.Metadata = &ConnectorAppMetadata{}
		}
		if connector.Metadata.HomepageURL == nil || strings.TrimSpace(*connector.Metadata.HomepageURL) == "" {
			connector.Metadata.HomepageURL = &homepageURL
		}
	}
	if docsURL := strings.TrimSpace(tool.DocsURL); docsURL != "" {
		if connector.Metadata == nil {
			connector.Metadata = &ConnectorAppMetadata{}
		}
		if connector.Metadata.DocsURL == nil || strings.TrimSpace(*connector.Metadata.DocsURL) == "" {
			connector.Metadata.DocsURL = &docsURL
		}
	}
}

func ConnectorsWithEnabledState(connectors []ConnectorAppInfo, userConfig *ConnectorAppsConfig, requirements *ConnectorAppsConfig) []ConnectorAppInfo {
	out := cloneConnectorApps(connectors)
	for i := range out {
		if userConfig != nil {
			out[i].IsEnabled = appEnabled(userConfig, out[i].ID, out[i].IsEnabled)
		}
		if requirements != nil {
			if app, ok := requirements.Apps[out[i].ID]; ok && app.Enabled != nil && !*app.Enabled {
				out[i].IsEnabled = false
			}
		}
	}
	return out
}

func ConnectorsWithPluginSources(connectors []ConnectorAppInfo, provenance *ConnectorPluginProvenance) []ConnectorAppInfo {
	out := cloneConnectorApps(connectors)
	for i := range out {
		out[i].PluginDisplayNames = provenance.Names(out[i].ID)
	}
	return out
}

type ConnectorCacheKey struct {
	ChatGPTBaseURL     string
	AccountID          *string
	ChatGPTUserID      *string
	IsWorkspaceAccount bool
}

type ConnectorCache struct {
	mu    sync.Mutex
	now   func() time.Time
	ttl   time.Duration
	entry *cacheEntry
}

type cacheEntry struct {
	key        ConnectorCacheKey
	expiresAt  time.Time
	connectors []ConnectorAppInfo
}

func NewConnectorCache(ttl time.Duration) *ConnectorCache {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &ConnectorCache{ttl: ttl, now: time.Now}
}

func (c *ConnectorCache) SetClock(clock func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if clock == nil {
		c.now = time.Now
		return
	}
	c.now = clock
}

func (c *ConnectorCache) Read(key ConnectorCacheKey) ([]ConnectorAppInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entry == nil || !cacheKeysEqual(&c.entry.key, &key) || !c.now().Before(c.entry.expiresAt) {
		if c.entry != nil && !c.now().Before(c.entry.expiresAt) {
			c.entry = nil
		}
		return nil, false
	}
	return cloneConnectorApps(c.entry.connectors), true
}

func (c *ConnectorCache) Write(key ConnectorCacheKey, connectors []ConnectorAppInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entry = &cacheEntry{
		key:        cloneConnectorCacheKey(key),
		expiresAt:  c.now().Add(c.ttl),
		connectors: cloneConnectorApps(connectors),
	}
}

func isSyntheticLink(meta map[string]any) bool {
	if meta == nil {
		return false
	}
	if value := firstConnectorMetaBool(nestedConnectorMetaMap(meta), meta, "synthetic_link", "syntheticLink"); value != nil {
		return *value
	}
	return false
}

func connectorMetaMap(value any) map[string]any {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]any:
		return cloneConnectorAnyMap(typed)
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	default:
		return nil
	}
}

func nestedConnectorMetaMap(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	for _, key := range []string{"_codex_apps", "codex_apps", "codexApps"} {
		if nested := connectorMetaMap(meta[key]); nested != nil {
			return nested
		}
	}
	return nil
}

func firstConnectorMetaMap(primary map[string]any, secondary map[string]any, keys ...string) map[string]any {
	if values := connectorMetaMapForKeys(primary, keys...); values != nil {
		return values
	}
	return connectorMetaMapForKeys(secondary, keys...)
}

func connectorMetaMapForKeys(values map[string]any, keys ...string) map[string]any {
	if values == nil {
		return nil
	}
	for _, key := range keys {
		if nested := connectorMetaMap(values[key]); nested != nil {
			return nested
		}
	}
	return nil
}

func connectorMetaString(values map[string]any, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func firstConnectorMetaBool(primary map[string]any, secondary map[string]any, keys ...string) *bool {
	if value, ok := connectorMetaBool(primary, keys...); ok {
		return value
	}
	if value, ok := connectorMetaBool(secondary, keys...); ok {
		return value
	}
	return nil
}

func connectorMetaBool(values map[string]any, keys ...string) (*bool, bool) {
	if values == nil {
		return nil, false
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case bool:
			v := typed
			return &v, true
		case string:
			switch strings.ToLower(strings.TrimSpace(typed)) {
			case "true":
				v := true
				return &v, true
			case "false":
				v := false
				return &v, true
			}
		}
	}
	return nil, false
}

func connectorMetaStrings(values map[string]any, keys ...string) []string {
	if values == nil {
		return nil
	}
	for _, key := range keys {
		value, ok := values[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return mergeStrings(nil, typed)
		case []any:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if text, ok := item.(string); ok {
					out = append(out, text)
				}
			}
			return mergeStrings(nil, out)
		case string:
			return mergeStrings(nil, []string{typed})
		}
	}
	return nil
}

func mergeConnectorMetaMaps(base map[string]any, override map[string]any) map[string]any {
	if base == nil {
		return connectorMetaMap(override)
	}
	out := cloneConnectorAnyMap(base)
	for key, value := range override {
		if existing, ok := out[key].(map[string]any); ok {
			if nested := connectorMetaMap(value); nested != nil {
				out[key] = mergeConnectorMetaMaps(existing, nested)
				continue
			}
		}
		out[key] = value
	}
	return out
}

func appEnabled(config *ConnectorAppsConfig, id string, fallback bool) bool {
	if config == nil {
		return fallback
	}
	enabled := config.DefaultEnabled
	if !enabled && config.Apps == nil {
		enabled = fallback
	}
	if app, ok := config.Apps[id]; ok && app.Enabled != nil {
		enabled = *app.Enabled
	}
	return enabled
}

func mergeStrings(base []string, values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(values))
	for _, value := range append(append([]string(nil), base...), values...) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func cloneConnectorApps(connectors []ConnectorAppInfo) []ConnectorAppInfo {
	out := make([]ConnectorAppInfo, len(connectors))
	for i := range connectors {
		out[i] = cloneConnectorAppInfo(connectors[i])
	}
	return out
}

func cloneConnectorAppInfo(connector ConnectorAppInfo) ConnectorAppInfo {
	if connector.Branding != nil {
		branding := *connector.Branding
		branding.IconURL = cloneConnectorStringPtr(branding.IconURL)
		branding.Color = cloneConnectorStringPtr(branding.Color)
		connector.Branding = &branding
	}
	if connector.Metadata != nil {
		metadata := *connector.Metadata
		metadata.HomepageURL = cloneConnectorStringPtr(metadata.HomepageURL)
		metadata.DocsURL = cloneConnectorStringPtr(metadata.DocsURL)
		connector.Metadata = &metadata
	}
	connector.PluginDisplayNames = append([]string(nil), connector.PluginDisplayNames...)
	return connector
}

func cloneConnectorCacheKey(key ConnectorCacheKey) ConnectorCacheKey {
	key.AccountID = cloneConnectorStringPtr(key.AccountID)
	key.ChatGPTUserID = cloneConnectorStringPtr(key.ChatGPTUserID)
	return key
}

func cloneConnectorAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if nested, ok := value.(map[string]any); ok {
			out[key] = cloneConnectorAnyMap(nested)
			continue
		}
		out[key] = value
	}
	return out
}

func cacheKeysEqual(left *ConnectorCacheKey, right *ConnectorCacheKey) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.ChatGPTBaseURL == right.ChatGPTBaseURL &&
		stringPtrEqual(left.AccountID, right.AccountID) &&
		stringPtrEqual(left.ChatGPTUserID, right.ChatGPTUserID) &&
		left.IsWorkspaceAccount == right.IsWorkspaceAccount
}

func stringPtrEqual(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func cloneConnectorStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
