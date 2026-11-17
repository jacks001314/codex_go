package config

import (
	"fmt"
)

// StatusLineProviderSelection is the saved `tui.status_line_provider` choice.
// It is either the built-in status line or one MCP server/tool identity.
type StatusLineProviderSelection struct {
	Default bool
	Server  string
	Tool    string
}

// Clone preserves a nil pointer and copies the value otherwise.
func (s *StatusLineProviderSelection) Clone() *StatusLineProviderSelection {
	if s == nil {
		return nil
	}
	cloned := *s
	return &cloned
}

// ConfigValue is the TOML/JSON value written back to config.toml.
func (s *StatusLineProviderSelection) ConfigValue() any {
	if s == nil || s.Default {
		return "default"
	}
	return map[string]any{"server": s.Server, "tool": s.Tool}
}

// Validate enforces the upstream untagged selection: the literal `default` or
// a table with exactly `server` and `tool` string fields.
func (s *StatusLineProviderSelection) Validate() error {
	if s == nil {
		return nil
	}
	if s.Default {
		if s.Server != "" || s.Tool != "" {
			return fmt.Errorf("statusline provider default cannot name a server or tool")
		}
		return nil
	}
	return validateStatusLineProviderValue(map[string]any{"server": s.Server, "tool": s.Tool}, "tui.status_line_provider")
}

// StatusLineProviderSelectionFromValue parses one config value.
func StatusLineProviderSelectionFromValue(value any) (*StatusLineProviderSelection, error) {
	if text, ok := value.(string); ok {
		if text == "default" {
			return &StatusLineProviderSelection{Default: true}, nil
		}
		return nil, configUntaggedEnumError("StatusLineSelection", "tui.status_line_provider")
	}
	table, ok := value.(map[string]any)
	if !ok {
		return nil, configUntaggedEnumError("StatusLineSelection", "tui.status_line_provider")
	}
	server, serverOK := table["server"].(string)
	tool, toolOK := table["tool"].(string)
	if !serverOK || !toolOK || len(table) != 2 {
		return nil, configUntaggedEnumError("StatusLineSelection", "tui.status_line_provider")
	}
	return &StatusLineProviderSelection{Server: server, Tool: tool}, nil
}

// TUIStatusLineProvider returns the effective local statusline provider choice.
func (c *Config) TUIStatusLineProvider() *StatusLineProviderSelection {
	if c == nil {
		return nil
	}
	tui, ok := c.Values["tui"].(map[string]any)
	if !ok {
		return nil
	}
	value, ok := tui["status_line_provider"]
	if !ok {
		return nil
	}
	selection, err := StatusLineProviderSelectionFromValue(value)
	if err != nil {
		return nil
	}
	return selection
}

// StatusLineProviderConfigEdit is the persistence edit for the saved selection.
func StatusLineProviderConfigEdit(selection *StatusLineProviderSelection) (*ConfigEdit, error) {
	if selection == nil {
		return nil, fmt.Errorf("statusline provider selection is required")
	}
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	return &ConfigEdit{
		KeyPath:       "tui.status_line_provider",
		Value:         selection.ConfigValue(),
		MergeStrategy: MergeReplace,
	}, nil
}

func validateStatusLineProviderValue(value any, path string) error {
	if _, err := StatusLineProviderSelectionFromValue(value); err != nil {
		if configErr, ok := err.(*ConfigValueError); ok {
			return &ConfigValueError{message: configErr.message, path: path}
		}
		return err
	}
	return nil
}

// layerAllowsStatusLineProvider mirrors Rust #61211/#61915
// ConfigLayerSource::allows_status_line_provider: project settings and packaged
// defaults cannot select a status line provider, every other source can.
func layerAllowsStatusLineProvider(source LayerSource) bool {
	switch source.Type {
	case LayerSourcePackagedDefaults, LayerSourceProject:
		return false
	default:
		return true
	}
}

// statusLineProviderValidity mirrors Rust's status_line_provider_is_valid: it
// reports whether the layer carries a parseable `tui.status_line_provider` and
// whether the key is present at all.
func statusLineProviderValidity(values map[string]any) (valid bool, present bool) {
	if values == nil {
		return false, false
	}
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return false, false
	}
	value, ok := tui["status_line_provider"]
	if !ok {
		return false, false
	}
	_, err := StatusLineProviderSelectionFromValue(value)
	return err == nil, true
}

// effectiveStatusLineLayerValues mirrors Rust's ConfigLayerSource::effective_values
// (#61915): layers that cannot select a provider contribute no value, and an
// invalid selection becomes "default" so the built-in status line wins while the
// layer keeps its precedence. The caller's values are never mutated: the stored
// layer keeps the configured choice for diagnostics and `config/read`.
func effectiveStatusLineLayerValues(source LayerSource, values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return values
	}
	if _, ok := tui["status_line_provider"]; !ok {
		return values
	}
	effective := cloneMap(values)
	tui = effective["tui"].(map[string]any)
	if !layerAllowsStatusLineProvider(source) {
		delete(tui, "status_line_provider")
		if len(tui) == 0 {
			delete(effective, "tui")
		}
		return effective
	}
	if valid, _ := statusLineProviderValidity(values); !valid {
		tui["status_line_provider"] = "default"
	}
	return effective
}

// applyEffectiveStatusLineProvider rewrites an invalid effective selection (for
// example an invalid `-c tui.status_line_provider=...` session flag) to the
// built-in status line, mirroring Rust's effective values (#61915). Raw layers
// keep the configured choice.
func applyEffectiveStatusLineProvider(values map[string]any) {
	if values == nil {
		return
	}
	valid, present := statusLineProviderValidity(values)
	if !present || valid {
		return
	}
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return
	}
	tui["status_line_provider"] = "default"
}

// statusLineProviderWarning mirrors Rust's status_line_provider_warning: normal
// mode reports the winning (highest-precedence) provider-capable selection when
// it is invalid and falls back to the built-in status line; strict mode reports
// the first invalid selection in any source as a fatal error.
func statusLineProviderWarning(layers []Layer, strict bool) (string, error) {
	for _, layer := range layersHighToLow(layers) {
		if !strict && !layerAllowsStatusLineProvider(layer.Name) {
			continue
		}
		values, _ := layer.Config.(map[string]any)
		valid, present := statusLineProviderValidity(values)
		if !present {
			continue
		}
		if !strict {
			if valid {
				return "", nil
			}
			return fmt.Sprintf("Invalid `tui.status_line_provider` in %s. Using the built-in status line.",
				boundedConfigLabel(formatConfigLayerSource(layer.Name))), nil
		}
		if !valid {
			return "", fmt.Errorf("Invalid `tui.status_line_provider` in %s.",
				boundedConfigLabel(formatConfigLayerSource(layer.Name)))
		}
	}
	return "", nil
}

// formatConfigLayerSource mirrors Rust's format_config_layer_source.
func formatConfigLayerSource(source LayerSource) string {
	switch source.Type {
	case LayerSourcePackagedDefaults:
		return fmt.Sprintf("packaged defaults (%s)", source.File)
	case LayerSourceMDM:
		return fmt.Sprintf("MDM (%s:%s)", source.Domain, source.Key)
	case LayerSourceSystem:
		return fmt.Sprintf("system (%s)", source.File)
	case LayerSourceEnterpriseManaged:
		return fmt.Sprintf("enterprise-managed (%s, %s)", source.Name, source.ID)
	case LayerSourceUser:
		return fmt.Sprintf("user (%s)", source.File)
	case LayerSourceProject:
		// Rust formats `project ({folder}/{CONFIG_TOML_FILE})`, so the separator
		// before the file name is always a forward slash.
		return fmt.Sprintf("project (%s/config.toml)", source.DotCodexFolder)
	case LayerSourceSessionFlags:
		return "session-flags"
	case LayerSourceLegacyManagedConfigFromFile:
		return fmt.Sprintf("legacy managed_config.toml (%s)", source.File)
	case LayerSourceLegacyManagedConfigFromMDM:
		return "legacy managed_config.toml (MDM)"
	default:
		return string(source.Type)
	}
}

func isStatusLineProviderPath(path []string) bool {
	return len(path) >= 2 && path[len(path)-2] == "tui" && path[len(path)-1] == "status_line_provider"
}
