package tea

import (
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/chatwidget"
)

func daybreakSlashModel(t *testing.T, featureOn, signedIn bool, catalog []codextui.ModelPickerOption, enabled bool) *Model {
	t.Helper()
	accountDisplay := ""
	if signedIn {
		accountDisplay = "user@example.com (Plus)"
	}
	state := codextui.NewState(&codextui.Options{
		Model:             "gpt-5",
		Provider:          "openai",
		AccountDisplay:    accountDisplay,
		HasChatGPTAccount: signedIn,
	})
	state.SetThreadID("thread-daybreak")
	model := NewModel(state, Options{
		Width:              100,
		Height:             30,
		HasChatGPTAccount:  signedIn,
		FeatureSettings:    map[string]bool{"cli_daybreak": featureOn},
		ModelPickerOptions: catalog,
	})
	model.daybreakEnabled = enabled
	return model
}

func slashPopupItemByName(model *Model, name string) (slashCommandPopupItem, bool) {
	for _, item := range model.slashPopupCatalog() {
		if item.Name == name {
			return item, true
		}
	}
	return slashCommandPopupItem{}, false
}

// Rust #51207: cli_daybreak hides /daybreak from the slash catalog, and #49858
// selects the row description from the live preference plus catalog availability.
func TestDaybreakSlashCatalogVisibilityAndDescriptionLikeRust(t *testing.T) {
	blue := []codextui.ModelPickerOption{{ID: "gpt-5", AvailableCyberPrograms: []string{"standard", "daybreak_blue"}}}
	standardOnly := []codextui.ModelPickerOption{{ID: "gpt-5", AvailableCyberPrograms: []string{"standard"}}}
	tests := []struct {
		name        string
		featureOn   bool
		signedIn    bool
		catalog     []codextui.ModelPickerOption
		enabled     bool
		wantVisible bool
		wantDesc    string
	}{
		{
			name:      "feature disabled",
			featureOn: false,
			signedIn:  true,
			catalog:   blue,
		},
		{
			name:      "feature disabled with stale preference",
			featureOn: false,
			signedIn:  true,
			catalog:   blue,
			enabled:   true,
		},
		{
			name:      "account ineligible",
			featureOn: true,
			catalog:   blue,
		},
		{
			name:        "eligible with Daybreak catalog",
			featureOn:   true,
			signedIn:    true,
			catalog:     blue,
			wantVisible: true,
			wantDesc:    "Enable broader access for cybersecurity work",
		},
		{
			name:        "eligible without Daybreak catalog",
			featureOn:   true,
			signedIn:    true,
			catalog:     standardOnly,
			wantVisible: true,
			wantDesc:    "Learn about broader access for cybersecurity work",
		},
		{
			name:        "eligible with unknown catalog",
			featureOn:   true,
			signedIn:    true,
			wantVisible: true,
			wantDesc:    "Manage broader access for cybersecurity work",
		},
		{
			name:        "already enabled",
			featureOn:   true,
			signedIn:    true,
			catalog:     blue,
			enabled:     true,
			wantVisible: true,
			wantDesc:    "Disable broader access for cybersecurity work",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := daybreakSlashModel(t, tc.featureOn, tc.signedIn, tc.catalog, tc.enabled)
			item, ok := slashPopupItemByName(model, "daybreak")
			if ok != tc.wantVisible {
				t.Fatalf("/daybreak visible = %v, want %v", ok, tc.wantVisible)
			}
			if ok && item.Description != tc.wantDesc {
				t.Fatalf("/daybreak description = %q, want %q", item.Description, tc.wantDesc)
			}
		})
	}
}

// Rust #51207: dispatch returns silently when cli_daybreak is off.
func TestDaybreakCommandSilentWhenFeatureDisabledLikeRust(t *testing.T) {
	model := daybreakSlashModel(t, false, true, []codextui.ModelPickerOption{{
		ID:                     "gpt-5",
		AvailableCyberPrograms: []string{"standard", "daybreak_blue"},
	}}, false)
	updates := 0
	model.onUpdateDaybreak = func(string, bool) error {
		updates++
		return nil
	}
	beforeMessages := len(model.State.Messages)
	beforeNotice := model.notice
	model.applyDaybreakCommand()
	if model.daybreakEnabled || updates != 0 {
		t.Fatalf("disabled daybreak = %v updates=%d", model.daybreakEnabled, updates)
	}
	if len(model.State.Messages) != beforeMessages || model.notice != beforeNotice {
		t.Fatal("disabled /daybreak produced a visible side effect")
	}
}

// Rust #51207 set_feature_enabled: toggling cli_daybreak clears the live
// preference and refreshes the status surfaces; a settings reload does the same.
func TestCliDaybreakFeatureToggleClearsPreferenceLikeRust(t *testing.T) {
	model := daybreakSlashModel(t, true, true, []codextui.ModelPickerOption{{
		ID:                     "gpt-5",
		AvailableCyberPrograms: []string{"standard", "daybreak_blue"},
	}}, true)
	model.ensureStatusControls()
	model.syncStatusControlsRuntime()
	if value, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineDaybreak); !ok || value != "Daybreak on" {
		t.Fatalf("status line daybreak = %q ok=%v, want Daybreak on", value, ok)
	}

	model.setExperimentalFeatures([]chatwidget.ExperimentalFeatureOption{{
		Key:            "cli_daybreak",
		Enabled:        false,
		DefaultEnabled: false,
	}})
	if model.daybreakEnabled {
		t.Fatal("cli_daybreak toggle did not clear the live preference")
	}
	model.syncStatusControlsRuntime()
	if value, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineDaybreak); !ok || value != "Daybreak off" {
		t.Fatalf("status line daybreak = %q ok=%v, want Daybreak off", value, ok)
	}

	model.featureSettings = map[string]bool{"cli_daybreak": true}
	model.daybreakEnabled = true
	model.applyLocalSettingsValues(SettingsWriteResult{FeatureSettings: map[string]bool{"cli_daybreak": false}})
	if model.daybreakEnabled {
		t.Fatal("settings reload did not clear the live preference")
	}
}
