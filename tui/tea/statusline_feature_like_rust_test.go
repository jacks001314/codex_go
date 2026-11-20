package tea

import (
	"testing"

	"codex_go/config"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust #62387 startup::statusline_enabled reads
// `features.tui_extensions_statusline` out of the app server's config/read
// response and treats a missing key as off, so the launch gate never falls back
// to the TUI client's own configuration file.
func TestCustomStatuslineGateFollowsServerStartupConfigLikeRust(t *testing.T) {
	cases := []struct {
		name            string
		featureSettings map[string]bool
		want            bool
	}{
		{name: "enabled", featureSettings: map[string]bool{customStatuslineFeatureKey: true}, want: true},
		{name: "disabled", featureSettings: map[string]bool{customStatuslineFeatureKey: false}, want: false},
		{name: "absent", featureSettings: map[string]bool{"plugins": true}, want: false},
		{name: "no settings", featureSettings: nil, want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			model := NewModel(codextui.NewState(nil), Options{
				Width:           100,
				Height:          30,
				FeatureSettings: testCase.featureSettings,
			})
			if got := model.customStatuslineEnabled(); got != testCase.want {
				t.Fatalf("customStatuslineEnabled() = %t, want %t", got, testCase.want)
			}
			if got := model.statuslineProviderPickerEnabled(); got != testCase.want {
				t.Fatalf("statuslineProviderPickerEnabled() = %t, want %t", got, testCase.want)
			}
		})
	}
}

// Rust #62387: `/experimental` carries the opt-in the toggle is saved from, and
// saving it only changes the launch that reads the server configuration next.
func TestExperimentalStatuslineToggleAppliesOnNextLaunchLikeRust(t *testing.T) {
	menu := chatwidget.NewExperimentalFeaturesView(nil)
	var entry *chatwidget.ExperimentalFeatureOption
	for index := range menu.Items {
		if menu.Items[index].Key == customStatuslineFeatureKey {
			entry = &menu.Items[index]
		}
	}
	if entry == nil {
		t.Fatal("/experimental does not list the custom status line")
	}
	if entry.Name != "Custom status line" || entry.Description != "Restart the TUI, then use /statusline." {
		t.Fatalf("menu entry = %q/%q", entry.Name, entry.Description)
	}

	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.setExperimentalFeatures([]chatwidget.ExperimentalFeatureOption{{Key: customStatuslineFeatureKey, Enabled: true}})
	if !model.featureSettings[customStatuslineFeatureKey] {
		t.Fatal("the toggle did not record the saved feature value")
	}
	if model.customStatuslineEnabled() || model.statuslineProviderPickerEnabled() {
		t.Fatal("the toggle moved the launch gate before the restart")
	}

	enabled := NewModel(codextui.NewState(nil), Options{
		Width:           100,
		Height:          30,
		FeatureSettings: map[string]bool{customStatuslineFeatureKey: true},
	})
	enabled.setExperimentalFeatures([]chatwidget.ExperimentalFeatureOption{{Key: customStatuslineFeatureKey, Enabled: false}})
	if !enabled.customStatuslineEnabled() {
		t.Fatal("saving the opt-out moved this launch's gate")
	}
}

// Rust #62387: the gate is a property of the launch, so switching conversations
// or reloading the client preferences keeps it.
func TestCustomStatuslineGateSurvivesThreadSwitchLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-one")
	state.SetStatus("idle")
	model := NewModel(state, Options{
		Width:           100,
		Height:          30,
		FeatureSettings: map[string]bool{customStatuslineFeatureKey: true},
		StatusLineProvider: &config.StatusLineProviderSelection{
			Server: "fixture",
			Tool:   "line",
		},
	})
	model.switchThreadID("thread-two")
	model.applyLocalSettingsValues(SettingsWriteResult{FeatureSettings: map[string]bool{customStatuslineFeatureKey: false}})
	if !model.customStatuslineEnabled() {
		t.Fatal("a thread change or local settings reload cleared the launch gate")
	}
}

// Rust #62387: startup::statusline_enabled reads the gate out of the app
// server's config/read response, so a launch that could not read it (`None`)
// keeps the gate off even when the TUI client's own configuration enables the
// feature.
func TestCustomStatuslineGateUsesServerSnapshotLikeRust(t *testing.T) {
	off := false
	model := NewModel(codextui.NewState(nil), Options{
		Width:                    100,
		Height:                   30,
		FeatureSettings:          map[string]bool{customStatuslineFeatureKey: true},
		StatuslineFeatureEnabled: &off,
	})
	if model.customStatuslineEnabled() {
		t.Fatal("the client configuration overrode the server snapshot")
	}

	on := true
	model = NewModel(codextui.NewState(nil), Options{
		Width:                    100,
		Height:                   30,
		FeatureSettings:          map[string]bool{customStatuslineFeatureKey: false},
		StatuslineFeatureEnabled: &on,
	})
	if !model.customStatuslineEnabled() {
		t.Fatal("the server snapshot did not enable the gate")
	}
}
