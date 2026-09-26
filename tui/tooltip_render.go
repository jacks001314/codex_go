package tui

import "strings"

// This file ports Rust's tooltip rendering (`codex-rs/tui/src/tooltips.rs`):
// the `{key:context.action}` placeholder substitution and the resolved tip pool
// the startup and session tips draw from. Rust skips any tip whose placeholder
// is malformed or whose action has no current binding, and without a runtime
// keymap only the key-free tips remain.

// TooltipTemplates returns the catalog templates in file order (Rust's
// `tooltip_templates`). The templates still carry their `{key:...}`
// placeholders; use RenderTooltip to resolve them.
func TooltipTemplates() []string {
	return DefaultTooltips()
}

// ResolvedTooltips returns the local tip pool in catalog order with the current
// keybindings applied (Rust's `resolved_tooltips`): a tip with an invalid or
// unbound shortcut is omitted, and a nil keymap keeps only the key-free tips.
func ResolvedTooltips(keymap *KeymapConfig) []string {
	templates := TooltipTemplates()
	resolved := make([]string, 0, len(templates))
	for _, template := range templates {
		if tip, ok := RenderTooltip(template, keymap); ok {
			resolved = append(resolved, tip)
		}
	}
	return resolved
}

// RenderTooltip mirrors Rust's `render_tooltip`: every `{key:context.action}`
// placeholder is replaced with the current primary binding rendered as a padded
// code span. ok is false when a placeholder is malformed or its action has no
// binding, in which case the caller must skip the tip.
func RenderTooltip(template string, keymap *KeymapConfig) (string, bool) {
	var rendered strings.Builder
	rest := template
	for {
		index := strings.Index(rest, "{key:")
		if index < 0 {
			break
		}
		after := rest[index+len("{key:"):]
		close := strings.Index(after, "}")
		if close < 0 {
			return "", false
		}
		placeholder := after[:close]
		context, action, ok := strings.Cut(placeholder, ".")
		if !ok || context == "" || action == "" {
			return "", false
		}
		label, ok := tooltipPrimaryHint(keymap, context, action)
		if !ok {
			return "", false
		}
		rendered.WriteString(rest[:index])
		// A key or two-key chord can contain literal backticks; use a padded
		// code span (Rust `format!("`` {} ``", hint.display_label())`).
		rendered.WriteString("`` " + label + " ``")
		rest = after[close+1:]
	}
	rendered.WriteString(rest)
	return rendered.String(), true
}

// tooltipPrimaryHint resolves the primary shortcut for one placeholder. Rust
// requires both a runtime keymap and a known catalog action before it renders a
// hint, and an unbound action drops the whole tip.
func tooltipPrimaryHint(keymap *KeymapConfig, context string, action string) (string, bool) {
	if keymap == nil {
		return "", false
	}
	if _, known := FindKeymapAction(context, action); !known {
		return "", false
	}
	// ResolvedKeymapBindings' third result reports a *custom* binding, not
	// availability: a default binding returns `false` with the default specs and
	// a suppressed/unknown action returns no specs. Availability is therefore
	// "has at least one spec".
	bindings, _, _ := ResolvedKeymapBindings(keymap, context, action)
	if len(bindings) == 0 {
		return "", false
	}
	spec := strings.TrimSpace(bindings[0])
	if spec == "" {
		return "", false
	}
	return keybindingDisplayLabel(spec), true
}

// keybindingDisplayLabel renders a binding spec the way Rust's
// `ShortcutHint::display_label` does: a two-key chord joins the key labels with
// a space, and each key renders its modifiers in ctrl/shift/alt order followed
// by the key name.
func keybindingDisplayLabel(spec string) string {
	parts := strings.Fields(spec)
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		labels = append(labels, keybindingKeyLabel(part))
	}
	return strings.Join(labels, " ")
}

func keybindingKeyLabel(part string) string {
	modifiers := map[string]bool{}
	key := ""
	for _, segment := range strings.Split(part, "-") {
		switch segment {
		case "ctrl", "control":
			modifiers["ctrl"] = true
		case "alt", "option":
			modifiers["alt"] = true
		case "shift":
			modifiers["shift"] = true
		default:
			if key == "" {
				key = segment
			} else {
				key += "-" + segment
			}
		}
	}
	var label strings.Builder
	for _, modifier := range []struct {
		name  string
		label string
	}{{"ctrl", "ctrl"}, {"shift", "shift"}, {"alt", AltKeyLabel()}} {
		if modifiers[modifier.name] {
			label.WriteString(modifier.label)
			label.WriteString("+")
		}
	}
	label.WriteString(keybindingKeyNameLabel(key))
	return label.String()
}

// keybindingKeyNameLabel mirrors Rust's `KeyCode` display arm: the named keys
// that render differently from their binding spec, and lowercased names
// otherwise.
func keybindingKeyNameLabel(key string) string {
	switch key {
	case "up":
		return "\u2191"
	case "down":
		return "\u2193"
	case "left":
		return "\u2190"
	case "right":
		return "\u2192"
	case "page-up", "pageup", "pgup":
		return "pgup"
	case "page-down", "pagedown", "pgdn":
		return "pgdn"
	case "minus":
		return "-"
	default:
		return key
	}
}
