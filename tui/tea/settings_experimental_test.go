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
	// Rust ExperimentalFeaturesView::write: a default-enabled feature keeps an
	// explicit false so the default cannot re-enable it, while a default-off
	// feature drops the override.
	if value, ok := values["features.enable_request_compression"]; !ok || value != false {
		t.Fatalf("default-enabled disable write = %#v", values)
	}
	if value, ok := values["features.network_proxy"]; !ok || value != nil {
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

// TestExperimentalFeatureEditMatchesRust covers the edit rules: the generic
// popup path persists an explicit false when disabling a default-enabled
// feature and clears the override when disabling a default-off one (Rust
// ExperimentalFeaturesView::write, as pinned by
// experimental_features_tests::experimental_feature_writes_use_server_defaults...),
// the daemon opt-out always stays explicit (#46117), and the unmigrated controls
// clear it when disabling a default-off feature (build_feature_enabled_edit).
func TestExperimentalFeatureEditMatchesRust(t *testing.T) {
	cases := []struct {
		name         string
		item         chatwidget.ExperimentalFeatureOption
		wantKeyPath  string
		wantValueNil bool
		wantValue    any
	}{
		{name: "enable generic", item: chatwidget.ExperimentalFeatureOption{Key: "beta", Enabled: true}, wantKeyPath: "features.beta", wantValue: true},
		{name: "disable default-on generic", item: chatwidget.ExperimentalFeatureOption{Key: "beta", Enabled: false, DefaultEnabled: true}, wantKeyPath: "features.beta", wantValue: false},
		{name: "disable default-off generic", item: chatwidget.ExperimentalFeatureOption{Key: "beta", Enabled: false}, wantKeyPath: "features.beta", wantValueNil: true},
		{name: "disable daemon_auto_start stays explicit", item: chatwidget.ExperimentalFeatureOption{Key: "daemon_auto_start", Enabled: false}, wantKeyPath: "features.daemon_auto_start", wantValue: false},
		{name: "enable idle sleep", item: chatwidget.ExperimentalFeatureOption{Key: "prevent_idle_sleep", Enabled: true}, wantKeyPath: "features.prevent_idle_sleep", wantValue: true},
		{name: "disable idle sleep", item: chatwidget.ExperimentalFeatureOption{Key: "prevent_idle_sleep", Enabled: false}, wantKeyPath: "features.prevent_idle_sleep", wantValueNil: true},
		{name: "disable guardian default-on", item: chatwidget.ExperimentalFeatureOption{Key: "guardian_approval", Enabled: false, DefaultEnabled: true}, wantKeyPath: "features.guardian_approval", wantValue: false},
	}
	for _, testCase := range cases {
		edit := experimentalFeatureEdit(testCase.item)
		if edit.KeyPath != testCase.wantKeyPath {
			t.Fatalf("%s: key path = %q, want %q", testCase.name, edit.KeyPath, testCase.wantKeyPath)
		}
		if testCase.wantValueNil {
			if edit.Value != nil {
				t.Fatalf("%s: value = %#v, want nil", testCase.name, edit.Value)
			}
			continue
		}
		if edit.Value != testCase.wantValue {
			t.Fatalf("%s: value = %#v, want %#v", testCase.name, edit.Value, testCase.wantValue)
		}
	}
}

// TestModelExperimentalPopupSavesLikeRust covers Rust
// ExperimentalFeaturesView's save flow: read-only rows refuse toggles, an
// in-flight save blocks toggles and keeps the popup open with the saving status,
// and a clean readback closes it.
func TestModelExperimentalPopupSavesLikeRust(t *testing.T) {
	var writes [][]SettingsEdit
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{
		OnReadExperimentalFeatures: func(string) ([]ExperimentalFeatureEntry, error) {
			return []ExperimentalFeatureEntry{
				{Name: "beta_feature", DisplayName: "Beta feature", Stage: "beta", Enabled: false},
				{Name: "guardian_approval", DisplayName: "Guardian approval", Stage: "beta", Enabled: true, DefaultEnabled: true},
			}, nil
		},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{FeatureSettings: map[string]bool{"beta_feature": true, "guardian_approval": true}}, nil
		},
	})
	typeText(t, model, "/experimental")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(model.experimentalItems) != 2 || model.modal == nil {
		t.Fatalf("popup state = %#v modal=%#v", model.experimentalItems, model.modal)
	}
	if model.experimentalItems[0].Writable == false {
		t.Fatalf("beta feature should be writable: %#v", model.experimentalItems[0])
	}
	if model.experimentalItems[1].Writable {
		t.Fatalf("guardian approval should be read-only: %#v", model.experimentalItems[1])
	}

	// The writable row toggles.
	model.Update(key(bubbletea.KeySpace))
	if !model.experimentalItems[0].Enabled {
		t.Fatal("writable row did not toggle")
	}
	// The read-only row can be highlighted but refuses to toggle.
	model.Update(key(bubbletea.KeyDown))
	if model.modal.selected != 1 {
		t.Fatalf("selection = %d, want the read-only row", model.modal.selected)
	}
	model.Update(key(bubbletea.KeySpace))
	if !model.experimentalItems[1].Enabled {
		t.Fatal("read-only row toggled")
	}
	model.Update(key(bubbletea.KeyUp))
	if model.modal.selected != 0 {
		t.Fatalf("selection = %d, want the writable row", model.modal.selected)
	}

	_, saveCmd := model.Update(key(bubbletea.KeyEnter))
	if !model.experimentalFeaturesSaving {
		t.Fatal("save did not start")
	}
	if model.modal == nil {
		t.Fatal("popup closed before the write resolved")
	}
	if model.experimentalFeaturesStatus != "Saving experimental features…" {
		t.Fatalf("saving status = %q", model.experimentalFeaturesStatus)
	}
	// A toggle during the save is refused.
	model.Update(key(bubbletea.KeySpace))
	if !model.experimentalItems[0].Enabled {
		t.Fatal("toggle applied while a save was in flight")
	}

	runTeaCmd(t, model, saveCmd)
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "features.beta_feature" || writes[0][0].Value != true {
		t.Fatalf("writes = %#v", writes)
	}
	if model.modal != nil {
		t.Fatal("popup stayed open after a clean readback")
	}
	if model.experimentalFeaturesSaving || len(model.experimentalFeatureUnconfirmed) != 0 {
		t.Fatalf("save state = saving:%v unconfirmed:%#v", model.experimentalFeaturesSaving, model.experimentalFeatureUnconfirmed)
	}
}

// TestModelExperimentalPopupRetainsUnconfirmedOnFailureLikeRust covers Rust's
// unconfirmed retry: a failed save keeps the popup open with the error status,
// retains the dirty keys, and a later accept re-sends them even after the user
// reverts the row to its baseline.
func TestModelExperimentalPopupRetainsUnconfirmedOnFailureLikeRust(t *testing.T) {
	attempt := 0
	var writes [][]SettingsEdit
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{
		OnReadExperimentalFeatures: func(string) ([]ExperimentalFeatureEntry, error) {
			return []ExperimentalFeatureEntry{
				{Name: "beta_feature", DisplayName: "Beta feature", Stage: "beta", Enabled: false},
			}, nil
		},
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			attempt++
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			if attempt == 1 {
				return SettingsWriteResult{}, errors.New("write failed")
			}
			return SettingsWriteResult{FeatureSettings: map[string]bool{"beta_feature": false}}, nil
		},
	})
	typeText(t, model, "/experimental")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	model.Update(key(bubbletea.KeySpace))
	_, saveCmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, saveCmd)
	if model.modal == nil {
		t.Fatal("popup closed after a failed save")
	}
	if model.experimentalFeaturesSaving {
		t.Fatal("saving flag stayed set after a failure")
	}
	if model.experimentalFeaturesStatus != "write failed" {
		t.Fatalf("failure status = %q", model.experimentalFeaturesStatus)
	}
	if len(model.experimentalFeatureUnconfirmed) != 1 || model.experimentalFeatureUnconfirmed[0] != "beta_feature" {
		t.Fatalf("unconfirmed = %#v", model.experimentalFeatureUnconfirmed)
	}

	// Reverting the row to its baseline still resends the corrective write.
	model.Update(key(bubbletea.KeySpace))
	if model.experimentalItems[0].Enabled {
		t.Fatal("row did not revert")
	}
	_, retryCmd := model.Update(key(bubbletea.KeyEnter))
	if !model.experimentalFeaturesSaving {
		t.Fatal("retry did not start")
	}
	runTeaCmd(t, model, retryCmd)
	// The corrective write reverts a default-off feature to the server default
	// by removing the key.
	if len(writes) != 2 || len(writes[1]) != 1 || writes[1][0].KeyPath != "features.beta_feature" || writes[1][0].Value != nil {
		t.Fatalf("retry writes = %#v", writes)
	}
	if model.modal != nil {
		t.Fatal("popup stayed open after the successful retry")
	}
}
