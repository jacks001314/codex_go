package appserver

import (
	"testing"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/mcp"
)

func boolPtrAppPolicy(value bool) *bool { return &value }

// TestFilterCodexAppsRuntimeToolsHonorsManagedRequirementsLikeRust covers the
// managed requirement path: a Cloud/MDM layer can disable an app that the user
// config leaves enabled.
func TestFilterCodexAppsRuntimeToolsHonorsManagedRequirementsLikeRust(t *testing.T) {
	disabled := false
	enabled := true
	tool := codexAppsTool("files/read", "drive", boolPtrAppPolicy(true))
	cfg := &config.Config{
		Values: map[string]any{"apps": map[string]any{"drive": map[string]any{"enabled": true}}},
		Requirements: &config.ConfigRequirements{Apps: apps.AppsRequirements{
			"drive": {Enabled: &disabled},
		}},
	}
	if got := filterCodexAppsRuntimeTools([]mcp.RuntimeToolInfo{tool}, cfg); len(got) != 0 {
		t.Fatalf("managed disablement ignored: %#v", got)
	}
	cfg.Requirements = &config.ConfigRequirements{Apps: apps.AppsRequirements{
		"drive": {Enabled: &enabled},
	}}
	if got := filterCodexAppsRuntimeTools([]mcp.RuntimeToolInfo{tool}, cfg); len(got) != 1 {
		t.Fatalf("managed enablement rejected the tool: %#v", got)
	}
}

func codexAppsTool(name string, connectorID string, modelVisible *bool) mcp.RuntimeToolInfo {
	return mcp.RuntimeToolInfo{
		ServerName:  mcp.RuntimeCodexAppsMCPServerName,
		ConnectorID: connectorID,
		Tool: mcp.RuntimeTool{
			Name:         name,
			ModelVisible: modelVisible,
		},
	}
}

// TestFilterCodexAppsRuntimeToolsLikeRust mirrors Rust
// mcp_tool_exposure::filter_codex_apps_mcp_tools: only model-visible Codex Apps
// tools with a connector id that the app policy allows reach the model.
func TestFilterCodexAppsRuntimeToolsLikeRust(t *testing.T) {
	visible := true
	hidden := false
	tests := []struct {
		name   string
		tools  []mcp.RuntimeToolInfo
		values map[string]any
		want   []string
	}{
		{
			name: "non-app tools pass through untouched",
			tools: []mcp.RuntimeToolInfo{
				{ServerName: "docs", Tool: mcp.RuntimeTool{Name: "search"}},
				codexAppsTool("files/read", "drive", &visible),
			},
			want: []string{"search", "files/read"},
		},
		{
			name:  "app tool without a connector is dropped",
			tools: []mcp.RuntimeToolInfo{codexAppsTool("files/read", "", &visible)},
			want:  nil,
		},
		{
			name:  "app tool hidden from the model is dropped",
			tools: []mcp.RuntimeToolInfo{codexAppsTool("files/read", "drive", &hidden)},
			want:  nil,
		},
		{
			name:  "app tool with a default model visibility is kept",
			tools: []mcp.RuntimeToolInfo{codexAppsTool("files/read", "drive", nil)},
			want:  []string{"files/read"},
		},
		{
			name:  "per-tool disable drops the tool",
			tools: []mcp.RuntimeToolInfo{codexAppsTool("files/read", "drive", &visible)},
			values: map[string]any{"apps": map[string]any{
				"drive": map[string]any{"tools": map[string]any{"files/read": map[string]any{"enabled": false}}},
			}},
			want: nil,
		},
		{
			name:  "connector disable drops the tool",
			tools: []mcp.RuntimeToolInfo{codexAppsTool("files/read", "drive", &visible)},
			values: map[string]any{"apps": map[string]any{
				"drive": map[string]any{"enabled": false},
			}},
			want: nil,
		},
		{
			name:  "default disable keeps an explicitly enabled app",
			tools: []mcp.RuntimeToolInfo{codexAppsTool("files/read", "drive", &visible)},
			values: map[string]any{"apps": map[string]any{
				"_default": map[string]any{"enabled": false},
				"drive":    map[string]any{"enabled": true},
			}},
			want: []string{"files/read"},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := (*config.Config)(nil)
			if testCase.values != nil {
				cfg = &config.Config{Values: testCase.values}
			}
			got := filterCodexAppsRuntimeTools(testCase.tools, cfg)
			if len(got) != len(testCase.want) {
				t.Fatalf("tools = %#v, want %v", got, testCase.want)
			}
			for i := range got {
				if got[i].Tool.Name != testCase.want[i] {
					t.Fatalf("tool %d = %q, want %q", i, got[i].Tool.Name, testCase.want[i])
				}
			}
		})
	}
}

// TestFilterCodexAppsRuntimeToolsDestructivePolicyLikeRust pins the hint-based
// enablement the app policy adds on top of connector enablement.
func TestFilterCodexAppsRuntimeToolsDestructivePolicyLikeRust(t *testing.T) {
	tool := codexAppsTool("files/delete", "drive", boolPtrAppPolicy(true))
	tool.Tool.Annotations = &mcp.RuntimeToolAnnotations{DestructiveHint: boolPtrAppPolicy(true)}
	cfg := &config.Config{Values: map[string]any{"apps": map[string]any{
		"_default": map[string]any{"destructive_enabled": false},
	}}}
	if got := filterCodexAppsRuntimeTools([]mcp.RuntimeToolInfo{tool}, cfg); len(got) != 0 {
		t.Fatalf("destructive tool survived a destructive-disabled policy: %#v", got)
	}
	cfg.Values["apps"] = map[string]any{"_default": map[string]any{"destructive_enabled": true}}
	if got := filterCodexAppsRuntimeTools([]mcp.RuntimeToolInfo{tool}, cfg); len(got) != 1 {
		t.Fatalf("destructive tool dropped by an enabling policy: %#v", got)
	}
}
