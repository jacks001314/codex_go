package config

import "strings"

// ClientSearchProvider returns the highest configured tools.client_search_provider
// override. The value is optional: model metadata takes precedence when set.
func (c *Config) ClientSearchProvider() (string, bool) {
	if c == nil || c.Values == nil {
		return "", false
	}
	tools, ok := c.Values["tools"].(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := tools["client_search_provider"].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return strings.TrimSpace(value), true
}

// RequiresNativeSearch reports whether managed configuration prevents the
// standalone_web_search feature from being disabled. In that case search must
// stay on the native provider even when the model advertises MCP search.
func (c *Config) RequiresNativeSearch() bool {
	if c == nil || c.Requirements == nil || c.Requirements.FeatureRequirements == nil {
		return false
	}
	required, ok := c.Requirements.FeatureRequirements["standalone_web_search"]
	return ok && required
}
