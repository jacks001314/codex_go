package tui

import (
	"strings"
	"testing"
)

// TestKeymapConflictOrderFollowsRegistryLikeRust mirrors #41285: conflicts are
// reported in the action registry's context/action order, not map order.
func TestKeymapConflictOrderFollowsRegistryLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		context string
		first   string
		second  string
	}{
		{name: "editor", context: "editor", first: "move_left", second: "move_right"},
		{name: "vim", context: "vim_normal", first: "enter_insert", second: "append_after_cursor"},
		{name: "pager", context: "pager", first: "scroll_up", second: "scroll_down"},
		{name: "list", context: "list", first: "move_up", second: "move_down"},
		{name: "agents", context: "agents", first: "resume", second: "search"},
		{name: "approval", context: "approval", first: "approve", second: "deny"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			for attempt := 0; attempt < 10; attempt++ {
				config := NewKeymapConfig()
				if err := config.Set(testCase.context, testCase.second, []string{"f1"}); err != nil {
					t.Fatalf("set second binding: %v", err)
				}
				if err := config.Set(testCase.context, testCase.first, []string{"f1"}); err != nil {
					t.Fatalf("set first binding: %v", err)
				}
				err := config.Validate()
				want := "both " + testCase.context + "." + testCase.first +
					" and " + testCase.context + "." + testCase.second
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("attempt %d: Validate error = %v, want %q", attempt, err, want)
				}
			}
		})
	}
}

// TestKeymapConflictContextOrderFollowsRegistryLikeRust reports the earlier
// registry context even when a later context also has a conflict.
func TestKeymapConflictContextOrderFollowsRegistryLikeRust(t *testing.T) {
	config := NewKeymapConfig()
	for _, edit := range []struct {
		context string
		action  string
	}{
		{"pager", "scroll_down"},
		{"pager", "scroll_up"},
		{"editor", "move_right"},
		{"editor", "move_left"},
	} {
		if err := config.Set(edit.context, edit.action, []string{"f2"}); err != nil {
			t.Fatalf("set %s.%s: %v", edit.context, edit.action, err)
		}
	}
	err := config.Validate()
	if err == nil || !strings.Contains(err.Error(), "editor.move_left") {
		t.Fatalf("Validate error = %v, want the editor conflict first", err)
	}
}
