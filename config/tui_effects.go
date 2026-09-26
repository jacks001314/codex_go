package config

// TuiEffects mirrors Rust's `codex_config::types::TuiEffects`
// (`config/src/tui_effects.rs`): individual visual effects, each subordinate to
// the `tui.animations` master switch. Disabling an effect preserves its
// underlying activity.
type TuiEffects struct {
	// Starfield animates the composer starfield.
	Starfield bool
	// Shimmer shimmers status and loading text.
	Shimmer bool
	// Welcome animates the welcome artwork.
	Welcome bool
	// Effort animates reasoning-effort changes in the composer and footer.
	Effort bool
	// Progress animates activity bullets and loading spinners.
	Progress bool
	// Title blinks the terminal-title indicator when user action is required.
	Title bool
}

// DefaultTuiEffects mirrors `TuiEffects::default`: every effect is on.
func DefaultTuiEffects() TuiEffects {
	return TuiEffects{
		Starfield: true,
		Shimmer:   true,
		Welcome:   true,
		Effort:    true,
		Progress:  true,
		Title:     true,
	}
}

// TuiEffectsFromValues resolves the effective `[tui] effects` table. Rust's
// `TuiEffects` carries `#[serde(default)]`, so an absent table and an absent
// member both fall back to the enabled default; Go's `[tui]` sub-table is not
// value-validated, so a malformed member also falls back rather than failing the
// load.
func TuiEffectsFromValues(values map[string]any) TuiEffects {
	effects := DefaultTuiEffects()
	tui, ok := values["tui"].(map[string]any)
	if !ok {
		return effects
	}
	table, ok := tui["effects"].(map[string]any)
	if !ok {
		return effects
	}
	for key, target := range map[string]*bool{
		"starfield": &effects.Starfield,
		"shimmer":   &effects.Shimmer,
		"welcome":   &effects.Welcome,
		"effort":    &effects.Effort,
		"progress":  &effects.Progress,
		"title":     &effects.Title,
	} {
		if value, ok := table[key].(bool); ok {
			*target = value
		}
	}
	return effects
}

// TuiEffects resolves the loaded configuration's effect preferences.
func (c *Config) TuiEffects() TuiEffects {
	if c == nil {
		return DefaultTuiEffects()
	}
	return TuiEffectsFromValues(c.Values)
}
