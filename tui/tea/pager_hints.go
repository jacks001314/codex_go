package tea

import (
	"strings"

	codextui "codex_go/tui"
)

// pagerActionsHint mirrors Rust's pager_overlay::transcript::render_hints second
// row. Close/find use the configured pager bindings; browsing prompts is always
// Escape.
func (m *Model) pagerActionsHint() string {
	if m == nil {
		return "q close · f3 find · esc browse prompts"
	}
	closeLabel := m.pagerBindingLabel("close", "q")
	findLabel := m.pagerBindingLabel("find", "f3")
	return closeLabel + " close \u00b7 " + findLabel + " find \u00b7 esc browse prompts"
}

func (m *Model) pagerBindingLabel(action string, fallback string) string {
	if m == nil {
		return fallback
	}
	bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "pager", action)
	if len(bindings) == 0 {
		return fallback
	}
	if action == "find" {
		// Rust's default find hint is f3 even though the pager also accepts `/`.
		for _, binding := range bindings {
			if strings.EqualFold(strings.TrimSpace(binding), "f3") {
				return "f3"
			}
		}
	}
	return pagerKeySpecLabel(bindings[0])
}

func pagerKeySpecLabel(spec string) string {
	spec = strings.ToLower(strings.TrimSpace(spec))
	switch spec {
	case "":
		return ""
	case "up":
		return "\u2191"
	case "down":
		return "\u2193"
	case "page-up":
		return "pgup"
	case "page-down":
		return "pgdn"
	case "home":
		return "home"
	case "end":
		return "end"
	case "space":
		return "space"
	}
	if strings.HasPrefix(spec, "ctrl-") {
		return codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + strings.TrimPrefix(spec, "ctrl-")
	}
	if strings.HasPrefix(spec, "alt-") {
		return codextui.ModifierLabelPrefix(AltKeyLabel()) + strings.TrimPrefix(spec, "alt-")
	}
	return strings.ReplaceAll(spec, "-", "+")
}
