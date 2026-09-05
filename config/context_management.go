package config

// ContextManagementConfig mirrors the Rust features.context_management table
// (#42385): experimental context management is opt-in.
type ContextManagementConfig struct {
	ExperimentalMode bool
}

// ContextManagementConfig returns the resolved experimental context-management
// setting. The table is optional; absent configuration leaves it disabled.
func (c *Config) ContextManagementConfig() ContextManagementConfig {
	out := ContextManagementConfig{}
	if c == nil || c.Values == nil {
		return out
	}
	features, ok := c.Values["features"].(map[string]any)
	if !ok {
		return out
	}
	table, ok := features["context_management"].(map[string]any)
	if !ok {
		return out
	}
	if enabled, ok := table["experimental_mode"].(bool); ok {
		out.ExperimentalMode = enabled
	}
	return out
}
