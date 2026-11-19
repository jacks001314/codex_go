package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAppToolApprovalsReviewerConfigLikeRust covers Rust #62360: config.toml
// accepts approvals_reviewer on a single app tool, restricts it to the reviewer
// enum, and the config/read payload serializes the per-tool setting.
func TestAppToolApprovalsReviewerConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatalf("write config.toml error = %v", err)
		}
	}
	writeConfig(`
[apps.calendar.tools.calendar_create_event]
approvals_reviewer = "user"
`)
	cfg, err := LoadEffective(home, nil, nil, nil)
	if err != nil {
		t.Fatalf("LoadEffective() error = %v", err)
	}
	appsTable, ok := cfg.Values["apps"].(map[string]any)
	if !ok {
		t.Fatalf("apps config = %#v", cfg.Values["apps"])
	}
	calendar, ok := appsTable["calendar"].(map[string]any)
	if !ok {
		t.Fatalf("apps.calendar = %#v", appsTable)
	}
	tools, ok := calendar["tools"].(map[string]any)
	if !ok {
		t.Fatalf("apps.calendar.tools = %#v", calendar)
	}
	tool, ok := tools["calendar_create_event"].(map[string]any)
	if !ok || tool["approvals_reviewer"] != "user" {
		t.Fatalf("apps.calendar.tools.calendar_create_event = %#v", tools)
	}

	// The reviewer field is enum-shaped, so the serde-shape validator rejects an
	// unknown reviewer the way Rust's AppToolConfig deserializer does.
	invalid := map[string]any{"apps": map[string]any{"calendar": map[string]any{"tools": map[string]any{
		"calendar_create_event": map[string]any{"approvals_reviewer": "nobody"},
	}}}}
	if err := ValidateConfigValues(invalid); err == nil || !strings.Contains(err.Error(), "unknown variant") || !strings.Contains(err.Error(), "auto_review") {
		t.Fatalf("invalid reviewer error = %v", err)
	}
	valid := map[string]any{"apps": map[string]any{"calendar": map[string]any{"tools": map[string]any{
		"calendar_create_event": map[string]any{"approvals_reviewer": "auto_review"},
	}}}}
	if err := ValidateConfigValues(valid); err != nil {
		t.Fatalf("valid reviewer error = %v", err)
	}

	response := &ConfigReadResponse{
		Config: map[string]any{
			"apps": map[string]any{
				"calendar": map[string]any{
					"tools": map[string]any{
						"calendar_create_event": map[string]any{"approvals_reviewer": "user"},
						"calendar_delete_event": map[string]any{"approval_mode": "prompt"},
					},
				},
			},
		},
		Origins: map[string]LayerMetadata{},
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal ConfigReadResponse error = %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal ConfigReadResponse error = %v", err)
	}
	configMap, _ := root["config"].(map[string]any)
	apps := configMap["apps"].(map[string]any)
	tools = apps["calendar"].(map[string]any)["tools"].(map[string]any)
	create := tools["calendar_create_event"].(map[string]any)
	if create["approvals_reviewer"] != "user" {
		t.Fatalf("config/read tool reviewer = %#v", create)
	}
	remove := tools["calendar_delete_event"].(map[string]any)
	if value, ok := remove["approvals_reviewer"]; !ok || value != nil {
		t.Fatalf("config/read unset tool reviewer = %#v", remove)
	}
}
