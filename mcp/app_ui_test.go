package mcp

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestMCPAppUIFromToolMetaCoversDescriptorDeclarations mirrors Rust #45805 plus
// #48764: the resource URI may be declared as `ui.resourceUri`, `ui/resourceUri`,
// or `openai/outputTemplate`, and the presentation is only populated for an
// explicit `inline` or `fullscreen` preference. A missing or unsupported
// preference leaves the presentation unset while the resource URI is still
// reported independently.
func TestMCPAppUIFromToolMetaCoversDescriptorDeclarations(t *testing.T) {
	cases := []struct {
		name    string
		meta    any
		want    *McpAppUI
		wantURI string
	}{
		{
			name:    "ui resource uri without a preference",
			meta:    map[string]any{"ui": map[string]any{"resourceUri": "ui://widget/a"}},
			wantURI: "ui://widget/a",
		},
		{
			name:    "flat ui resource uri with fullscreen",
			meta:    map[string]any{"ui/resourceUri": "ui://widget/b", "openai/ui": map[string]any{"preferredModelDisplayMode": "fullscreen"}},
			want:    &McpAppUI{ResourceURI: "ui://widget/b", PreferredModelDisplayMode: McpAppDisplayModeFullscreen},
			wantURI: "ui://widget/b",
		},
		{
			name:    "explicit inline preference",
			meta:    map[string]any{"ui": map[string]any{"resourceUri": "ui://widget/g"}, "openai/ui": map[string]any{"preferredModelDisplayMode": "inline"}},
			want:    &McpAppUI{ResourceURI: "ui://widget/g", PreferredModelDisplayMode: McpAppDisplayModeInline},
			wantURI: "ui://widget/g",
		},
		{
			name:    "legacy output template",
			meta:    map[string]any{"openai/outputTemplate": "ui://widget/c"},
			wantURI: "ui://widget/c",
		},
		{
			name:    "unsupported display mode leaves the presentation unset",
			meta:    map[string]any{"ui": map[string]any{"resourceUri": "ui://widget/d"}, "openai/ui": map[string]any{"preferredModelDisplayMode": "sidebar"}},
			wantURI: "ui://widget/d",
		},
		{
			name:    "nested ui resource uri wins over the flat key",
			meta:    map[string]any{"ui": map[string]any{"resourceUri": "ui://widget/e"}, "ui/resourceUri": "ui://widget/f"},
			wantURI: "ui://widget/e",
		},
		{name: "result-only widget declares nothing", meta: map[string]any{"other": true}},
		{name: "no metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MCPAppUIFromToolMeta(tc.meta)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("MCPAppUIFromToolMeta() = %#v, want nil", got)
				}
			} else if got == nil || !reflect.DeepEqual(*got, *tc.want) {
				t.Fatalf("MCPAppUIFromToolMeta() = %#v, want %#v", got, tc.want)
			}
			if uri := MCPAppResourceURIFromToolMeta(tc.meta); uri != tc.wantURI {
				t.Fatalf("MCPAppResourceURIFromToolMeta() = %q, want %q", uri, tc.wantURI)
			}
			if tc.want == nil {
				return
			}
			// The wire shape stays camelCase, matching the v2 schema.
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if decoded["resourceUri"] != tc.want.ResourceURI ||
				decoded["preferredModelDisplayMode"] != string(tc.want.PreferredModelDisplayMode) {
				t.Fatalf("wire shape = %s", encoded)
			}
		})
	}
}

// TestApplyMCPAppOutputMetadataMirrorsTrustedCapture mirrors Rust's
// `McpToolCallItemMetadata::from_tool_metadata`: the widget presentation is
// captured for any server whose descriptor declares one, while the connector,
// link, app, and action identity only come from the trusted codex_apps server.
func TestApplyMCPAppOutputMetadataMirrorsTrustedCapture(t *testing.T) {
	appsMeta := map[string]any{
		"ui":        map[string]any{"resourceUri": "ui://widgets/calendar"},
		"openai/ui": map[string]any{"preferredModelDisplayMode": "fullscreen"},
		"_codex_apps": map[string]any{
			"link_id":      "link_calendar",
			"resource_uri": "ui://widgets/calendar/create_event",
		},
	}
	data := map[string]any{}
	ApplyMCPAppOutputMetadata(data, RuntimeCodexAppsMCPServerName, appsMeta, "calendar", "Calendar", "sample@openai-curated")
	appUI, ok := data["mcp_app_ui"].(McpAppUI)
	if !ok || appUI.ResourceURI != "ui://widgets/calendar" || appUI.PreferredModelDisplayMode != McpAppDisplayModeFullscreen {
		t.Fatalf("mcp_app_ui = %#v", data["mcp_app_ui"])
	}
	if data["mcp_app_resource_uri"] != "ui://widgets/calendar" {
		t.Fatalf("mcp_app_resource_uri = %#v", data["mcp_app_resource_uri"])
	}
	if data["connector_id"] != "calendar" || data["connector_name"] != "Calendar" ||
		data["link_id"] != "link_calendar" || data["action_name"] != "create_event" ||
		data["plugin_id"] != "sample@openai-curated" {
		t.Fatalf("app identity = %#v", data)
	}

	// A descriptor without an explicit display-mode preference keeps the
	// resource URI but leaves the presentation unset (Rust #48764), and a
	// non-Codex-Apps server never contributes the trusted identity.
	plain := map[string]any{}
	ApplyMCPAppOutputMetadata(plain, "plain_server", map[string]any{"ui": map[string]any{"resourceUri": "ui://widget/x"}}, "calendar", "Calendar", "sample@openai-curated")
	if _, present := plain["mcp_app_ui"]; present {
		t.Fatalf("mcp_app_ui should be unset without a preference: %#v", plain["mcp_app_ui"])
	}
	if plain["mcp_app_resource_uri"] != "ui://widget/x" {
		t.Fatalf("mcp_app_resource_uri = %#v", plain["mcp_app_resource_uri"])
	}
	if _, present := plain["connector_id"]; present {
		t.Fatalf("untrusted server contributed connector identity: %#v", plain)
	}
	// The owning plugin id is reported for every server.
	if plain["plugin_id"] != "sample@openai-curated" {
		t.Fatalf("plugin_id = %#v", plain["plugin_id"])
	}

	// No descriptor widget means no presentation at all.
	empty := map[string]any{}
	ApplyMCPAppOutputMetadata(empty, RuntimeCodexAppsMCPServerName, map[string]any{}, "calendar", "Calendar", "")
	if _, present := empty["mcp_app_ui"]; present {
		t.Fatalf("unexpected mcp_app_ui: %#v", empty)
	}
	if _, present := empty["mcp_app_resource_uri"]; present {
		t.Fatalf("unexpected mcp_app_resource_uri: %#v", empty)
	}
}

// TestMCPAppUIFromMetadataMapRoundTrips covers the decode path used when the
// captured presentation comes back from item data or rollout JSON.
func TestMCPAppUIFromMetadataMapRoundTrips(t *testing.T) {
	decoded := MCPAppUIFromMetadataMap(map[string]any{
		"resourceUri":               "ui://widget/round",
		"preferredModelDisplayMode": "fullscreen",
	})
	if decoded == nil || decoded.ResourceURI != "ui://widget/round" || decoded.PreferredModelDisplayMode != McpAppDisplayModeFullscreen {
		t.Fatalf("MCPAppUIFromMetadataMap() = %#v", decoded)
	}
	if got := MCPAppUIFromMetadataMap(map[string]any{"preferredModelDisplayMode": "fullscreen"}); got != nil {
		t.Fatalf("missing resource URI should decode to nil, got %#v", got)
	}
}

// TestMCPToolCallLinkIDReadsTrustedCodexAppsMeta mirrors Rust's link id lookup
// inside the Codex Apps `_codex_apps` metadata.
func TestMCPToolCallLinkIDReadsTrustedCodexAppsMeta(t *testing.T) {
	meta := map[string]any{"_codex_apps": map[string]any{"link_id": "link_calendar", "resource_uri": "ui://widgets/calendar/create_event"}}
	if got := MCPToolCallLinkID(meta); got != "link_calendar" {
		t.Fatalf("MCPToolCallLinkID() = %q", got)
	}
	if got := mcpToolCallActionName(meta); got != "create_event" {
		t.Fatalf("mcpToolCallActionName() = %q", got)
	}
	if got := MCPToolCallLinkID(map[string]any{"ui": map[string]any{"resourceUri": "ui://widget/a"}}); got != "" {
		t.Fatalf("MCPToolCallLinkID() = %q, want empty", got)
	}
}
