package config

// TuiRendering mirrors Rust's `codex_config::types::TuiRendering`
// (`config/src/tui_rendering.rs`): optional rich renderers, independent of
// animation effects. Disabled renderers preserve the original source.
type TuiRendering struct {
	// Mermaid renders Mermaid code blocks as diagrams.
	Mermaid bool
	// Math renders math expressions using Unicode notation.
	Math bool
	// Tables renders pipe tables, including tables inside Markdown fences.
	Tables bool
	// Lists renders Markdown bullets and task-list markers as Unicode symbols.
	Lists bool
}

// DefaultTuiRendering mirrors `TuiRendering::default`: every rich renderer is on.
func DefaultTuiRendering() TuiRendering {
	return TuiRendering{Mermaid: true, Math: true, Tables: true, Lists: true}
}

// TuiRenderingFromValues resolves the effective `[tui] rendering` table. Rust's
// `TuiRendering` carries `#[serde(default)]`, so an absent table and an absent
// member both fall back to the enabled default; Go's `[tui]` sub-table is not
// value-validated, so a malformed member also falls back rather than failing the
// load.
func TuiRenderingFromValues(values map[string]any) TuiRendering {
	rendering := DefaultTuiRendering()
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return rendering
	}
	table, ok := tui["rendering"].(map[string]any)
	if !ok {
		return rendering
	}
	for key, target := range map[string]*bool{
		"mermaid": &rendering.Mermaid,
		"math":    &rendering.Math,
		"tables":  &rendering.Tables,
		"lists":   &rendering.Lists,
	} {
		if value, ok := table[key].(bool); ok {
			*target = value
		}
	}
	return rendering
}

// TuiRendering resolves the loaded configuration's rendering preferences.
func (c *Config) TuiRendering() TuiRendering {
	if c == nil {
		return DefaultTuiRendering()
	}
	return TuiRenderingFromValues(c.Values)
}
