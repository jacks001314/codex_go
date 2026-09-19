package appserver

import (
	"strings"
	"testing"

	"codex_go/config"
)

// Mirrors Rust #46554: legacy Windows sandbox launches always use a private
// desktop. The removed `[windows] sandbox_private_desktop` setting (and its
// legacy `permissions` spelling) cannot opt out; the key is reported as an
// ignored configuration setting with the migration hint instead.
func TestWindowsSandboxPrivateDesktopIsAlwaysEnabledLikeRust(t *testing.T) {
	if !windowsSandboxPrivateDesktopForTurn() {
		t.Fatal("legacy Windows sandboxes must always use a private desktop")
	}
	for _, values := range []map[string]any{
		{"windows": map[string]any{"sandbox_private_desktop": false}},
		{"permissions": map[string]any{"windows_sandbox_private_desktop": false}},
	} {
		warning := config.IgnoredConfigWarning([]config.Layer{{
			Name:   config.LayerSource{Type: config.LayerSourceUser, File: "config.toml"},
			Config: values,
		}}, nil)
		if !strings.Contains(warning, "Remove windows.sandbox_private_desktop") {
			t.Fatalf("obsolete setting %#v produced warning %q", values, warning)
		}
	}
}
