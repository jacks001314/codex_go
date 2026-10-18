package app

import (
	"testing"

	"codex_go/config"
)

func TestInteractiveCopyOnSelectConfigLikeRust(t *testing.T) {
	if got := interactiveCopyOnSelect(nil); got == nil || *got != "auto" {
		t.Fatalf("default copy_on_select = %#v", got)
	}
	for raw, want := range map[string]string{
		"always":  "always",
		" ALWAYS": "always",
		"never":   "never",
		"bogus":   "auto",
	} {
		got := interactiveCopyOnSelect(map[string]any{"tui": map[string]any{"copy_on_select": raw}})
		if got == nil || *got != want {
			t.Fatalf("copy_on_select %q = %#v, want %q", raw, got, want)
		}
	}
	if got := interactiveCopyOnSelectValue(nil); got != "auto" {
		t.Fatalf("flattened default = %q", got)
	}
	if got := interactiveCopyOnSelectClearSelection(nil); got == nil || *got {
		t.Fatalf("default clear-after-copy = %#v", got)
	}
	enabled := interactiveCopyOnSelectClearSelection(map[string]any{
		"tui": map[string]any{"copy_on_select_clear_selection": true},
	})
	if enabled == nil || !*enabled {
		t.Fatalf("clear-after-copy = %#v", enabled)
	}
	if got := interactiveStartInVimMode(nil); got == nil || *got {
		t.Fatalf("default start-in-vim = %#v", got)
	}
	if got := interactiveStartInVimMode(map[string]any{"tui": map[string]any{"vim_mode_default": true}}); got == nil || !*got {
		t.Fatalf("start-in-vim = %#v", got)
	}
	if got := interactiveResumeCWD(nil); got == nil || *got != "ask" {
		t.Fatalf("default resume_cwd = %#v", got)
	}
	for raw, want := range map[string]string{
		"current": "current",
		"CURRENT": "current",
		"session": "session",
		"bogus":   "ask",
	} {
		got := interactiveResumeCWD(map[string]any{"tui": map[string]any{"resume_cwd": raw}})
		if got == nil || *got != want {
			t.Fatalf("resume_cwd %q = %#v, want %q", raw, got, want)
		}
	}

	settings := interactiveSettingsFromConfig(&config.Config{Values: map[string]any{
		"tui": map[string]any{
			"copy_on_select":                 "never",
			"copy_on_select_clear_selection": true,
			"vim_mode_default":               true,
			"resume_cwd":                     "session",
		},
	}})
	if settings.CopyOnSelect == nil || *settings.CopyOnSelect != "never" {
		t.Fatalf("settings copy_on_select = %#v", settings.CopyOnSelect)
	}
	if settings.CopyOnSelectClearSelection == nil || !*settings.CopyOnSelectClearSelection {
		t.Fatalf("settings clear-after-copy = %#v", settings.CopyOnSelectClearSelection)
	}
	if settings.StartInVimMode == nil || !*settings.StartInVimMode {
		t.Fatalf("settings start-in-vim = %#v", settings.StartInVimMode)
	}
	if settings.ResumeCWD == nil || *settings.ResumeCWD != "session" {
		t.Fatalf("settings resume_cwd = %#v", settings.ResumeCWD)
	}
}
