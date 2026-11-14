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

func validateIgnoredStatusLineProvider(values map[string]any) error {
	if values == nil {
		return nil
	}
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return nil
	}
	value, ok := tui["status_line_provider"]
	if !ok {
		return nil
	}
	return validateStatusLineProviderValue(value, "tui.status_line_provider")
}

func stripIgnoredStatusLineProvider(values map[string]any) {
	if values == nil {
		return
	}
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return
	}
	delete(tui, "status_line_provider")
	if len(tui) == 0 {
		delete(values, "tui")
	}
}

func isStatusLineProviderPath(path []string) bool {
	return len(path) >= 2 && path[len(path)-2] == "tui" && path[len(path)-1] == "status_line_provider"
}
