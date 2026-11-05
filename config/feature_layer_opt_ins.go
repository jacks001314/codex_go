package config

import (
	"sort"

	featureflags "codex_go/features"
)

// projectOverridesLegacySubagentOptOut mirrors Rust's managed_features.rs
// helper (#59476/#59496). Configuration layers are scanned from highest to
// lowest precedence. An explicit legacy collab opt-out ends the scan; enabled
// legacy flags do not, so a project's [agents].enabled opt-in can replace a
// strictly lower-priority opt-out.
func projectOverridesLegacySubagentOptOut(layers []Layer) bool {
	ordered := cloneLayers(layers)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Name.Precedence() < ordered[j].Name.Precedence()
	})
	projectEnabled := false
	for i := len(ordered) - 1; i >= 0; i-- {
		layer := ordered[i]
		if layer.DisabledReason != nil {
			continue
		}
		values, _ := layer.Config.(map[string]any)
		if featureLayerDisablesCollab(values) {
			return projectEnabled
		}
		if layer.Name.Type == LayerSourceProject && agentsExplicitlyEnabled(values) {
			projectEnabled = true
		}
	}
	return false
}

func featureLayerDisablesCollab(values map[string]any) bool {
	features, ok := values["features"].(map[string]any)
	if !ok {
		return false
	}
	for key, raw := range features {
		enabled, ok := raw.(bool)
		if !ok || enabled {
			continue
		}
		canonical, ok := featureflags.CanonicalKey(key)
		if ok && canonical == "multi_agent" {
			return true
		}
	}
	return false
}

func agentsExplicitlyEnabled(values map[string]any) bool {
	agents, ok := values["agents"].(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := agents["enabled"].(bool)
	return ok && enabled
}

// ApplyOverrides applies runtime overrides to the effective values and records
// them as a session-flags layer, keeping project agent opt-in precedence
// accurate after per-turn or CLI overrides.
func (c *Config) ApplyOverrides(overrides []Override) {
	if c == nil || len(overrides) == 0 {
		return
	}
	if c.Values == nil {
		c.Values = map[string]any{}
	}
	ApplyOverrides(c.Values, overrides)
	sessionValues := map[string]any{}
	ApplyOverrides(sessionValues, overrides)
	c.featureLayers = append(c.featureLayers, Layer{
		Name:   LayerSource{Type: LayerSourceSessionFlags},
		Config: sessionValues,
	})
}

// LegacyMultiAgentDisabledSources reports the saved canonical
// `features.multi_agent = false` settings from user, project, and managed
// requirement sources (Rust #59478). Values overridden by a higher-priority
// session layer remain visible here because this describes saved settings.
func (c *Config) LegacyMultiAgentDisabledSources() []string {
	if c == nil {
		return nil
	}
	ordered := cloneLayers(c.featureLayers)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Name.Precedence() < ordered[j].Name.Precedence()
	})
	var user, project *bool
	for i := range ordered {
		layer := ordered[i]
		if layer.DisabledReason != nil {
			continue
		}
		var setting **bool
		switch layer.Name.Type {
		case LayerSourceUser:
			setting = &user
		case LayerSourceProject:
			setting = &project
		default:
			continue
		}
		values, _ := layer.Config.(map[string]any)
		features, _ := values["features"].(map[string]any)
		if enabled, ok := features["multi_agent"].(bool); ok {
			value := enabled
			*setting = &value
		}
	}
	var managed *bool
	if c.Requirements != nil {
		if enabled, ok := c.Requirements.FeatureRequirements["multi_agent"]; ok {
			value := enabled
			managed = &value
		}
	}
	sources := make([]string, 0, 3)
	for _, source := range []struct {
		name    string
		enabled *bool
	}{
		{name: "user", enabled: user},
		{name: "project", enabled: project},
		{name: "managed_requirement", enabled: managed},
	} {
		if source.enabled != nil && !*source.enabled {
			sources = append(sources, source.name)
		}
	}
	return sources
}
