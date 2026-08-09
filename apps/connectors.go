package apps

import (
	"os"
	"sort"
	"strings"
)

type AppsConfig struct {
	Default *AppsDefaultConfig
	Apps    map[string]AppConfig
}

type AppConfig struct {
	Enabled *bool
}

func MergeConnectors(connectors []AppEntry, accessibleConnectors []AppEntry) []AppEntry {
	merged := make(map[string]AppEntry, len(connectors)+len(accessibleConnectors))
	for i := range connectors {
		connector := connectorBaseApp(connectors[i])
		if connector.ID == "" {
			continue
		}
		merged[connector.ID] = connector
	}
	for i := range accessibleConnectors {
		connector := connectorAccessibleApp(accessibleConnectors[i])
		if connector.ID == "" {
			continue
		}
		existing, ok := merged[connector.ID]
		if !ok {
			merged[connector.ID] = connector
			continue
		}
		existing.IsAccessible = true
		existing.IsEnabled = connector.IsEnabled
		existing.Enabled = connector.Enabled
		existing.EnabledExplicit = connector.EnabledExplicit
		if connector.InstallURL != nil && strings.TrimSpace(*connector.InstallURL) != "" {
			existing.InstallURL = cloneStringPtr(connector.InstallURL)
		} else {
			installURL := ConnectorInstallURL(connector.ID, connector.ID)
			existing.InstallURL = &installURL
		}
		if strings.TrimSpace(connector.Name) != "" && connector.Name != connector.ID {
			existing.Name = connector.Name
		}
		if existing.Description == nil && connector.Description != nil {
			existing.Description = cloneStringPtr(connector.Description)
		}
		if existing.LogoURL == nil && connector.LogoURL != nil {
			existing.LogoURL = cloneStringPtr(connector.LogoURL)
		}
		if existing.LogoURLDark == nil && connector.LogoURLDark != nil {
			existing.LogoURLDark = cloneStringPtr(connector.LogoURLDark)
		}
		if len(existing.IconAssets) == 0 && len(connector.IconAssets) > 0 {
			existing.IconAssets = cloneStringMap(connector.IconAssets)
		}
		if len(existing.IconDarkAssets) == 0 && len(connector.IconDarkAssets) > 0 {
			existing.IconDarkAssets = cloneStringMap(connector.IconDarkAssets)
		}
		if existing.DistributionChannel == nil && connector.DistributionChannel != nil {
			existing.DistributionChannel = cloneStringPtr(connector.DistributionChannel)
		}
		if existing.Branding == nil && connector.Branding != nil {
			existing.Branding = cloneAppAny(connector.Branding)
		}
		if existing.AppMetadata == nil && connector.AppMetadata != nil {
			existing.AppMetadata = cloneAppAny(connector.AppMetadata)
		}
		existing.PluginDisplayNames = mergeAppStrings(existing.PluginDisplayNames, connector.PluginDisplayNames)
		merged[connector.ID] = existing
	}
	out := make([]AppEntry, 0, len(merged))
	for _, connector := range merged {
		if connector.InstallURL == nil {
			installURL := ConnectorInstallURL(connector.Name, connector.ID)
			connector.InstallURL = &installURL
		}
		connector.PluginDisplayNames = mergeAppStrings(connector.PluginDisplayNames, nil)
		out = append(out, cloneApp(connector))
	}
	sortAppsByAccessibilityAndName(out)
	return out
}

func MergePluginConnectors(connectors []AppEntry, pluginConnectors []PluginConnector) []AppEntry {
	out := cloneApps(connectors)
	byID := make(map[string]int, len(out))
	for i := range out {
		if id := strings.TrimSpace(out[i].ID); id != "" {
			byID[id] = i
		}
	}
	for i := range pluginConnectors {
		id := strings.TrimSpace(pluginConnectors[i].ID)
		if id == "" {
			continue
		}
		pluginApp := pluginConnectorToAppInfo(pluginConnectors[i])
		if index, ok := byID[id]; ok {
			out[index] = mergePluginConnectorMetadata(out[index], pluginApp)
			continue
		}
		byID[id] = len(out)
		out = append(out, pluginApp)
	}
	sortAppsByAccessibilityAndName(out)
	return out
}

func mergePluginConnectorMetadata(existing AppEntry, pluginApp AppEntry) AppEntry {
	if strings.TrimSpace(existing.Name) == "" || existing.Name == existing.ID {
		existing.Name = pluginApp.Name
	}
	if existing.Description == nil {
		existing.Description = cloneStringPtr(pluginApp.Description)
	}
	if existing.InstallURL == nil {
		existing.InstallURL = cloneStringPtr(pluginApp.InstallURL)
	}
	if existing.LogoURL == nil {
		existing.LogoURL = cloneStringPtr(pluginApp.LogoURL)
	}
	if existing.LogoURLDark == nil {
		existing.LogoURLDark = cloneStringPtr(pluginApp.LogoURLDark)
	}
	if existing.Branding == nil && pluginApp.Branding != nil {
		existing.Branding = cloneAppAny(pluginApp.Branding)
	}
	if existing.AppMetadata == nil && pluginApp.AppMetadata != nil {
		existing.AppMetadata = cloneAppAny(pluginApp.AppMetadata)
	}
	existing.PluginDisplayNames = mergeAppStrings(existing.PluginDisplayNames, pluginApp.PluginDisplayNames)
	return existing
}

func PluginConnectorToAppInfo(connectorID string) AppEntry {
	return pluginConnectorToAppInfo(PluginConnector{ID: connectorID})
}

func pluginConnectorToAppInfo(connector PluginConnector) AppEntry {
	connectorID := strings.TrimSpace(connector.ID)
	name := strings.TrimSpace(connector.Name)
	if name == "" {
		name = connectorID
	}
	installURL := cloneStringPtr(connector.InstallURL)
	if installURL == nil {
		value := ConnectorInstallURL(name, connectorID)
		installURL = &value
	}
	pluginDisplayNames := mergeAppStrings(nil, []string{connector.PluginDisplayName})
	return AppEntry{
		ID:                 connectorID,
		Name:               name,
		Description:        cloneStringPtr(connector.Description),
		InstallURL:         installURL,
		LogoURL:            cloneStringPtr(connector.LogoURL),
		LogoURLDark:        cloneStringPtr(connector.LogoURLDark),
		IsAccessible:       false,
		IsEnabled:          true,
		Enabled:            true,
		PluginDisplayNames: pluginDisplayNames,
	}
}

func ConnectorInstallURL(name string, connectorID string) string {
	baseURL := strings.TrimSpace(os.Getenv("CODEX_APP_SERVER_CHATGPT_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://chatgpt.com"
	}
	origin := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/backend-api")
	slug := ConnectorMentionSlugFromName(name)
	return origin + "/apps/" + slug + "/" + strings.TrimSpace(connectorID)
}

func ConnectorMentionSlugFromName(name string) string {
	name = strings.TrimSpace(name)
	var builder strings.Builder
	builder.Grow(len(name))
	for _, ch := range name {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			if ch >= 'A' && ch <= 'Z' {
				ch += 'a' - 'A'
			}
			builder.WriteRune(ch)
			continue
		}
		builder.WriteByte('-')
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return "app"
	}
	return slug
}

func WithAppEnabledState(connectors []AppEntry, userConfig *AppsConfig, requirements *AppsConfig) []AppEntry {
	out := cloneApps(connectors)
	for i := range out {
		if userConfig != nil {
			enabled := appEnabledFromConfig(userConfig, out[i].ID, out[i].IsEnabled || out[i].Enabled)
			out[i].IsEnabled = enabled
			out[i].Enabled = enabled
			out[i].EnabledExplicit = true
		}
		if requirements != nil {
			if app, ok := requirements.Apps[out[i].ID]; ok && app.Enabled != nil && !*app.Enabled {
				out[i].IsEnabled = false
				out[i].Enabled = false
				out[i].EnabledExplicit = true
			}
		}
	}
	return out
}

func AppsConfigFromValues(values map[string]any) *AppsConfig {
	if values == nil {
		return nil
	}
	raw, ok := values["apps"].(map[string]any)
	if !ok {
		return nil
	}
	out := &AppsConfig{Apps: map[string]AppConfig{}}
	if defaultValues, ok := raw["_default"].(map[string]any); ok {
		out.Default = &AppsDefaultConfig{Enabled: true, DestructiveEnabled: true, OpenWorldEnabled: true}
		if enabled, ok := defaultValues["enabled"].(bool); ok {
			out.Default.Enabled = enabled
		}
	}
	for key, value := range raw {
		key = strings.TrimSpace(key)
		if key == "" || key == "_default" {
			continue
		}
		table, ok := value.(map[string]any)
		if !ok {
			continue
		}
		app := AppConfig{}
		if enabled, ok := table["enabled"].(bool); ok {
			app.Enabled = boolPtrApps(enabled)
		}
		if app.Enabled != nil {
			out.Apps[key] = app
		}
	}
	if out.Default == nil && len(out.Apps) == 0 {
		return nil
	}
	return out
}

func mergeDirectorySnapshots(directory []AppEntry, local []AppEntry) []AppEntry {
	byID := make(map[string]AppEntry, len(directory)+len(local))
	order := make([]string, 0, len(directory)+len(local))
	for _, apps := range [][]AppEntry{directory, local} {
		for i := range apps {
			app := cloneApp(apps[i])
			if strings.TrimSpace(app.ID) == "" {
				continue
			}
			if _, exists := byID[app.ID]; !exists {
				order = append(order, app.ID)
			}
			byID[app.ID] = app
		}
	}
	out := make([]AppEntry, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

func mergeAccessibleSnapshots(primary []AppEntry, extra []AppEntry) []AppEntry {
	byID := make(map[string]AppEntry, len(primary)+len(extra))
	for _, apps := range [][]AppEntry{primary, extra} {
		for i := range apps {
			app := connectorAccessibleApp(apps[i])
			if app.ID == "" {
				continue
			}
			if existing, ok := byID[app.ID]; ok {
				byID[app.ID] = MergeConnectors([]AppEntry{existing}, []AppEntry{app})[0]
				continue
			}
			byID[app.ID] = app
		}
	}
	out := make([]AppEntry, 0, len(byID))
	for _, app := range byID {
		out = append(out, cloneApp(app))
	}
	sortAppsByAccessibilityAndName(out)
	return out
}

func filterAccessibleAppsForDirectory(accessible []AppEntry, directory []AppEntry) []AppEntry {
	if len(accessible) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(directory))
	for i := range directory {
		allowed[directory[i].ID] = true
	}
	out := make([]AppEntry, 0, len(accessible))
	for i := range accessible {
		if allowed[accessible[i].ID] {
			out = append(out, cloneApp(accessible[i]))
		}
	}
	return out
}

func accessibleStaticApps(apps []AppEntry) []AppEntry {
	out := make([]AppEntry, 0)
	for i := range apps {
		if apps[i].IsAccessible {
			out = append(out, cloneApp(apps[i]))
		}
	}
	return out
}

func connectorBaseApp(app AppEntry) AppEntry {
	app = cloneApp(app)
	app.ID = strings.TrimSpace(app.ID)
	app.Name = strings.TrimSpace(app.Name)
	if app.Name == "" {
		app.Name = app.ID
	}
	app.IsAccessible = false
	if !app.EnabledExplicit && !app.IsEnabled && !app.Enabled {
		app.IsEnabled = true
		app.Enabled = true
	}
	return app
}

func connectorAccessibleApp(app AppEntry) AppEntry {
	app = cloneApp(app)
	app.ID = strings.TrimSpace(app.ID)
	app.Name = strings.TrimSpace(app.Name)
	if app.Name == "" {
		app.Name = app.ID
	}
	app.IsAccessible = true
	if !app.EnabledExplicit && !app.IsEnabled && !app.Enabled {
		app.IsEnabled = true
		app.Enabled = true
	}
	return app
}

func sortAppsByAccessibilityAndName(apps []AppEntry) {
	sort.SliceStable(apps, func(i int, j int) bool {
		if apps[i].IsAccessible != apps[j].IsAccessible {
			return apps[i].IsAccessible
		}
		if apps[i].Name != apps[j].Name {
			return apps[i].Name < apps[j].Name
		}
		return apps[i].ID < apps[j].ID
	})
}

func appEnabledFromConfig(config *AppsConfig, id string, fallback bool) bool {
	if config == nil {
		return fallback
	}
	enabled := fallback
	if config.Default != nil {
		enabled = config.Default.Enabled
	}
	if app, ok := config.Apps[id]; ok && app.Enabled != nil {
		enabled = *app.Enabled
	}
	return enabled
}

func cloneApps(apps []AppEntry) []AppEntry {
	out := make([]AppEntry, len(apps))
	for i := range apps {
		out[i] = cloneApp(apps[i])
	}
	return out
}

func clonePluginConnectors(connectors []PluginConnector) []PluginConnector {
	out := make([]PluginConnector, 0, len(connectors))
	for i := range connectors {
		id := strings.TrimSpace(connectors[i].ID)
		if id == "" {
			continue
		}
		out = append(out, PluginConnector{
			ID:                id,
			Name:              strings.TrimSpace(connectors[i].Name),
			Description:       cloneStringPtr(connectors[i].Description),
			InstallURL:        cloneStringPtr(connectors[i].InstallURL),
			LogoURL:           cloneStringPtr(connectors[i].LogoURL),
			LogoURLDark:       cloneStringPtr(connectors[i].LogoURLDark),
			PluginDisplayName: strings.TrimSpace(connectors[i].PluginDisplayName),
		})
	}
	return out
}

func mergeAppStrings(base []string, values []string) []string {
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
	sort.Strings(out)
	return out
}

func cloneAnyMapApps(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case map[string]any:
			out[key] = cloneAnyMapApps(typed)
		case []any:
			items := make([]any, len(typed))
			for i := range typed {
				if nested, ok := typed[i].(map[string]any); ok {
					items[i] = cloneAnyMapApps(nested)
				} else {
					items[i] = typed[i]
				}
			}
			out[key] = items
		default:
			out[key] = typed
		}
	}
	return out
}

func boolPtrApps(value bool) *bool {
	return &value
}
