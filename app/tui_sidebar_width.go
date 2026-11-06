package app

import (
	"encoding/json"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
)

// interactiveSidebarWidthPercent resolves `tui.sidebar_width_percent` for the
// side-by-side conversation layout (Rust #59595). Zero means the default split.
func interactiveSidebarWidthPercent(root *cli.RootOptions) int {
	values := map[string]any{}
	if loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root)); err == nil && loaded != nil {
		values = loaded.Values
	}
	return sidebarWidthPercentFromValues(values)
}

func sidebarWidthPercentFromValues(values map[string]any) int {
	tuiTable, _ := values["tui"].(map[string]any)
	if tuiTable == nil {
		return 0
	}
	percent := 0
	switch value := tuiTable["sidebar_width_percent"].(type) {
	case int:
		percent = value
	case int64:
		percent = int(value)
	case float64:
		percent = int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err != nil {
			return 0
		}
		percent = int(parsed)
	default:
		return 0
	}
	if percent < 1 || percent > 99 {
		return 0
	}
	return percent
}
