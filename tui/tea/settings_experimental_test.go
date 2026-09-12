package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

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
		{Key: "enable_request_compression", Enabled: false, DefaultEnabled: true},
		{Key: "network_proxy", Enabled: false, DefaultEnabled: false},
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
		{Key: "enable_request_compression", Enabled: true, DefaultEnabled: true},
	})
	runTeaCmd(t, model, cmd)
	if len(writes) != 1 || writes[0][0].Value != true {
		t.Fatalf("enable write = %#v", writes)
	}
}

// TestModelExperimentalMenuUsesServerCatalogLikeRust covers the server-backed
// popup: /experimental starts in the loading state, queries the catalog for the
// current thread, lists only Beta-stage features with the server metadata, and
// reports the empty/failed discovery states.
func TestModelExperimentalMenuUsesServerCatalogLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	var requested []string
	entries := []ExperimentalFeatureEntry{
		{Name: "beta_feature", DisplayName: "Beta feature", Description: "Beta description", Enabled: true, DefaultEnabled: true, Stage: "beta"},
		{Name: "stable_feature", DisplayName: "Stable feature", Stage: "stable", Enabled: true},
		{Name: "removed_feature", DisplayName: "Removed feature", Stage: "removed"},
	}
	model := NewModel(state, Options{
		FeatureSettings: map[string]bool{"network_proxy": false},
		OnReadExperimentalFeatures: func(threadID string) ([]ExperimentalFeatureEntry, error) {
			requested = append(requested, threadID)
			return entries, nil
		},
	})

	typeText(t, model, "/experimental")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if !strings.Contains(model.View(), "Loading server experiments") {
		t.Fatalf("loading status missing:\n%s", model.View())
	}
	runTeaCmd(t, model, cmd)
	if len(requested) != 1 || requested[0] != "thread-1" {
		t.Fatalf("catalog requests = %#v", requested)
	}
	view := model.View()
	if !strings.Contains(view, "Beta feature") || !strings.Contains(view, "Beta description") {
		t.Fatalf("server metadata missing:\n%s", view)
	}
	if strings.Contains(view, "Stable feature") || strings.Contains(view, "Removed feature") {
		t.Fatalf("non-Beta stages listed:\n%s", view)
	}
	if strings.Contains(view, "Loading server experiments") {
		t.Fatalf("loading status not cleared:\n%s", view)
	}
	if len(model.experimentalItems) != 1 {
		t.Fatalf("experimental items = %#v", model.experimentalItems)
	}
	item := model.experimentalItems[0]
	if item.Key != "beta_feature" || item.Name != "Beta feature" || !item.Enabled || !item.DefaultEnabled {
		t.Fatalf("catalog item = %#v", item)
	}

	// A stale generation must not replace the current rows.
	model.applyExperimentalFeaturesResult(ExperimentalFeaturesResultMsg{
		Generation: model.experimentalFeaturesGeneration + 1,
		Features:   []ExperimentalFeatureEntry{{Name: "stale", Stage: "beta"}},
	})
	if len(model.experimentalItems) != 1 || model.experimentalItems[0].Key != "beta_feature" {
		t.Fatalf("stale catalog applied: %#v", model.experimentalItems)
	}

	// An empty catalog reports the empty status; a failure reports the retry
	// status.
	model.applyExperimentalFeaturesResult(ExperimentalFeaturesResultMsg{Generation: model.experimentalFeaturesGeneration})
	if len(model.experimentalItems) != 0 || !strings.Contains(model.View(), "No server experiments available.") {
		t.Fatalf("empty catalog state:\n%s", model.View())
	}
	model.applyExperimentalFeaturesResult(ExperimentalFeaturesResultMsg{
		Generation: model.experimentalFeaturesGeneration,
		Err:        errors.New("offline"),
	})
	if !strings.Contains(model.View(), "Server experiments unavailable. Reopen /experimental to retry") {
		t.Fatalf("failure status missing:\n%s", model.View())
	}
}

// TestModelExperimentalReadbackWarningLikeRust covers Rust
// experimental_features::write's readback warning: a saved value that differs
// from the selection (a higher-priority setting won) warns instead of reporting
// a plain save.
func TestModelExperimentalReadbackWarningLikeRust(t *testing.T) {
	newModel := func(readback bool) *Model {
		return NewModel(codextui.NewState(nil), Options{
			FeatureSettings: map[string]bool{"network_proxy": true},
			OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
				return SettingsWriteResult{FeatureSettings: map[string]bool{"network_proxy": readback}}, nil
			},
		})
	}
	overridden := newModel(true)
	cmd := overridden.setExperimentalFeatures([]chatwidget.ExperimentalFeatureOption{{
		Key: "network_proxy", Enabled: false,
	}})
	runTeaCmd(t, overridden, cmd)
	if !strings.Contains(overridden.notice, "A higher-priority setting may override them") {
		t.Fatalf("overridden notice = %q", overridden.notice)
	}
	if overridden.pendingExperimentalFeatureUpdates != nil {
		t.Fatalf("pending updates not cleared: %#v", overridden.pendingExperimentalFeatureUpdates)
	}
	applied := newModel(false)
	cmd = applied.setExperimentalFeatures([]chatwidget.ExperimentalFeatureOption{{
		Key: "network_proxy", Enabled: false,
	}})
	runTeaCmd(t, applied, cmd)
	if strings.Contains(applied.notice, "higher-priority") {
		t.Fatalf("unexpected warning: %q", applied.notice)
	}
	if !strings.Contains(applied.notice, "Feature network_proxy disabled.") {
		t.Fatalf("applied notice = %q", applied.notice)
	}
}
