package config

import (
	"os"
	"strings"
	"testing"
)

// Rust #63069: the table's root_agent field is the feature flag, and the
// independent subagents opt-in is exposed separately.
func TestSendMessageToUserAsyncFeatureSettingsLikeRust(t *testing.T) {
	cases := []struct {
		name          string
		value         any
		wantRoot      bool
		wantSubagents bool
	}{
		{name: "boolean shorthand", value: true, wantRoot: true},
		{name: "explicit root", value: map[string]any{"root_agent": true, "subagents": false}, wantRoot: true},
		{name: "subagents only", value: map[string]any{"root_agent": false, "subagents": true}, wantSubagents: true},
		{name: "enabled fallback", value: map[string]any{"enabled": true}, wantRoot: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := Config{Values: map[string]any{
				"features": map[string]any{"send_message_to_user_async": testCase.value},
			}}
			if got := cfg.FeatureSettings()["send_message_to_user_async"]; got != testCase.wantRoot {
				t.Fatalf("root feature = %v, want %v", got, testCase.wantRoot)
			}
			if got := cfg.SendMessageToUserAsyncSubagents(); got != testCase.wantSubagents {
				t.Fatalf("subagent feature = %v, want %v", got, testCase.wantSubagents)
			}
		})
	}
}

// Rust #63069: a boolean overlay updates root_agent without dropping the
// nested subagents setting, in either merge direction.
func TestMergeSendMessageToUserAsyncPreservesSubagentsLikeRust(t *testing.T) {
	dst := map[string]any{"features": map[string]any{
		"send_message_to_user_async": map[string]any{"root_agent": false, "subagents": true},
	}}
	mergeConfigMapsAt(dst, map[string]any{"features": map[string]any{
		"send_message_to_user_async": true,
	}}, nil)
	entry := dst["features"].(map[string]any)["send_message_to_user_async"].(map[string]any)
	if entry["root_agent"] != true || entry["subagents"] != true {
		t.Fatalf("merged entry = %#v", entry)
	}

	reverse := map[string]any{"features": map[string]any{"send_message_to_user_async": true}}
	mergeConfigMapsAt(reverse, map[string]any{"features": map[string]any{
		"send_message_to_user_async": map[string]any{"subagents": true},
	}}, nil)
	entry = reverse["features"].(map[string]any)["send_message_to_user_async"].(map[string]any)
	if entry["root_agent"] != true || entry["subagents"] != true {
		t.Fatalf("reverse merged entry = %#v", entry)
	}
}

func TestApplyEditSendMessageToUserAsyncPreservesSubagentsLikeRust(t *testing.T) {
	root := map[string]any{"features": map[string]any{
		"send_message_to_user_async": map[string]any{"root_agent": false, "subagents": true},
	}}
	applyEdit(root, &ConfigEdit{KeyPath: "features.send_message_to_user_async", Value: true})
	entry := root["features"].(map[string]any)["send_message_to_user_async"].(map[string]any)
	if entry["root_agent"] != true || entry["subagents"] != true {
		t.Fatalf("boolean edit entry = %#v", entry)
	}

	applyEdit(root, &ConfigEdit{
		KeyPath: "features.send_message_to_user_async",
		Value:   map[string]any{"root_agent": false},
	})
	entry = root["features"].(map[string]any)["send_message_to_user_async"].(map[string]any)
	if entry["root_agent"] != false || entry["subagents"] != true {
		t.Fatalf("toggle-table edit entry = %#v", entry)
	}
}

func TestSetFeatureSendMessageToUserAsyncPreservesSubagentsLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	path := ConfigPath(codexHome)
	contents := "[features.send_message_to_user_async]\nroot_agent = false\nsubagents = true\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := SetFeature(codexHome, "send_message_to_user_async", true); err != nil {
		t.Fatalf("SetFeature() error = %v", err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	text := string(updated)
	if !strings.Contains(text, "root_agent = true") || !strings.Contains(text, "subagents = true") {
		t.Fatalf("config = %s", text)
	}
	if strings.Contains(text, "enabled =") {
		t.Fatalf("generic enabled field written into the async-message table: %s", text)
	}
}
