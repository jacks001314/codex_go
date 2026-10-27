package app

import (
	"reflect"
	"testing"

	"codex_go/config"
)

// Rust's LocalSettings carries both ordered status-surface lists from
// `[tui]`. An absent key selects the built-in default, while an explicit empty
// list disables the surface and must survive the options handoff.
func TestInteractiveSettingsLoadsTerminalTitleItemsLikeRust(t *testing.T) {
	settings := interactiveSettingsFromConfig(&config.Config{Values: map[string]any{
		"tui": map[string]any{
			"status_line":    []any{"model", "run-state"},
			"terminal_title": []string{"activity", "thread-name", "project-name"},
		},
	}})
	if settings.StatusLineItems == nil || !reflect.DeepEqual(*settings.StatusLineItems, []string{"model", "run-state"}) {
		t.Fatalf("status line items = %#v", settings.StatusLineItems)
	}
	if settings.TerminalTitleItems == nil || !reflect.DeepEqual(*settings.TerminalTitleItems, []string{"activity", "thread-name", "project-name"}) {
		t.Fatalf("terminal title items = %#v", settings.TerminalTitleItems)
	}
	if got := optionalTUISettingList(settings.TerminalTitleItems); !reflect.DeepEqual(got, []string{"activity", "thread-name", "project-name"}) {
		t.Fatalf("terminal title options = %#v", got)
	}

	empty := interactiveSettingsFromConfig(&config.Config{Values: map[string]any{
		"tui": map[string]any{"terminal_title": []any{}},
	}})
	if empty.TerminalTitleItems == nil || len(*empty.TerminalTitleItems) != 0 {
		t.Fatalf("empty terminal title = %#v, want a configured empty list", empty.TerminalTitleItems)
	}
	if got := optionalTUISettingList(empty.TerminalTitleItems); got == nil || len(got) != 0 {
		t.Fatalf("empty terminal title options = %#v, want non-nil empty", got)
	}

	absent := interactiveSettingsFromConfig(&config.Config{Values: map[string]any{"tui": map[string]any{}}})
	if absent.TerminalTitleItems != nil || absent.StatusLineItems != nil {
		t.Fatalf("absent status surfaces = %#v / %#v, want nil defaults", absent.StatusLineItems, absent.TerminalTitleItems)
	}
}
