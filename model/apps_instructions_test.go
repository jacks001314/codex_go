package model

import (
	"strings"
	"testing"
)

func TestResolveAppsInstructionsLikeRust(t *testing.T) {
	bundled := ResolveAppsInstructions(nil, "codex_apps")
	for _, tc := range []struct {
		name, text string
		override   bool
	}{
		{"literal", "  {{mcp_server_name}}\r\n", true},
		{"clear", "", true},
		{"whitespace", " \r\n\t", false},
		{"800 bytes", strings.Repeat("é", 400), true},
		{"802 bytes", strings.Repeat("é", 401), false},
		{"ascii overflow", strings.Repeat("a", 801), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveAppsInstructions(&AppsMessages{UsageInstructions: &tc.text}, "codex_apps")
			if got.CatalogOverride != tc.override {
				t.Fatalf("override=%v want %v", got.CatalogOverride, tc.override)
			}
			want := bundled.UsageInstructions
			if tc.override {
				want = tc.text
			}
			if got.UsageInstructions != want {
				t.Fatalf("instructions=%q want %q", got.UsageInstructions, want)
			}
		})
	}
	if got := ResolveAppsInstructions(&AppsMessages{}, "codex_apps"); got != bundled {
		t.Fatal("missing field differs from bundled")
	}
	if !strings.Contains(bundled.UsageInstructions, "`codex_apps`") {
		t.Fatal("bundled namespace missing")
	}
}
