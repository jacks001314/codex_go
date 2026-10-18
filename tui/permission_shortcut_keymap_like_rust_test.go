package tui

import (
	"strings"
	"testing"
)

func TestPermissionShortcutBindingsRejectPrintableKeysLikeRust(t *testing.T) {
	for _, action := range []string{"previous_permission_mode", "next_permission_mode"} {
		for _, binding := range []string{"a", "shift-a", "2", "space"} {
			config := NewKeymapConfig()
			err := config.Set("chat", action, []string{binding})
			if err == nil || !strings.Contains(err.Error(), "printable") {
				t.Fatalf("%s=%s error = %v, want printable-key rejection", action, binding, err)
			}
		}
		config := NewKeymapConfig()
		if err := config.Set("chat", action, []string{"f10"}); err != nil {
			t.Fatalf("%s=f10 error = %v", action, err)
		}
	}
}
