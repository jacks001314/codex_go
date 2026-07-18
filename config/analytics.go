package config

type AnalyticsConfig map[string]any

func (c *Config) AnalyticsEnabled(defaultEnabled bool) bool {
	enabled := c.AnalyticsEnabledValue()
	if enabled == nil {
		return defaultEnabled
	}
	return *enabled
}

func (c *Config) AnalyticsEnabledValue() *bool {
	if c == nil || c.Values == nil {
		return nil
	}
	object, ok := c.Values["analytics"].(map[string]any)
	if !ok {
		return nil
	}
	enabled, ok := object["enabled"].(bool)
	if !ok {
		return nil
	}
	return &enabled
}
