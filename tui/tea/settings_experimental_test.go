package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// TestModelExperimentalWritesClearDefaultOverridesLikeRust covers Rust
// experimental_features::write's edit rule: enabling writes true, disabling a
// default-enabled feature clears the override (null), and disabling a
// default-off feature writes false.
func TestModelExperimentalWritesClearDefaultOverridesLikeRust(t *testing.T) {
	var writes [][]SettingsEdit
	model := NewModel(codextui.NewState(nil), Options{
		FeatureSettings: map[string]bool{
			"enable_request_compression": true, // default-enabled (stable)
			"network_proxy":              true, // default-off (experimental)
		},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{}, nil
		},
	})
	cmd := model.setExperimentalFeatures([]chatwidget.ExperimentalFeatureOption{
		{Key: "enable_request_compression", Enabled: false},
		{Key: "network_proxy", Enabled: false},
	})
	runTeaCmd(t, model, cmd)
	if len(writes) != 1 || len(writes[0]) != 2 {
		t.Fatalf("experimental writes = %#v", writes)
	}
	values := map[string]any{}
	for _, edit := range writes[0] {
		values[edit.KeyPath] = edit.Value
	}
	if value, ok := values["features.enable_request_compression"]; !ok || value != nil {
		t.Fatalf("default-enabled disable write = %#v", values)
	}
	if value, ok := values["features.network_proxy"]; !ok || value != false {
		t.Fatalf("default-off disable write = %#v", values)
	}

	// Enabling always writes true, even for a default-enabled feature.
	writes = nil
	cmd = model.setExperimentalFeatures([]chatwidget.ExperimentalFeatureOption{
		{Key: "enable_request_compression", Enabled: true},
	})
	runTeaCmd(t, model, cmd)
	if len(writes) != 1 || writes[0][0].Value != true {
		t.Fatalf("enable write = %#v", writes)
	}
}
