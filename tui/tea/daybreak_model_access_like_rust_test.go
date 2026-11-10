package tea

import (
	"errors"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// daybreakAccessModel builds a ChatGPT thread with Daybreak on and a catalog the
// caller controls, matching the state Rust #60526 reacts to.
func daybreakAccessModel(t *testing.T, modelID string, catalog []codextui.ModelPickerOption, persist func(string, bool) error) (*Model, *[]bool) {
	t.Helper()
	updates := []bool{}
	state := codextui.NewState(&codextui.Options{
		Model:             modelID,
		Provider:          "openai",
		AccountDisplay:    "user@example.com (Plus)",
		HasChatGPTAccount: true,
	})
	state.SetThreadID("thread-daybreak")
	model := NewModel(state, Options{
		Width:              80,
		Height:             24,
		HasChatGPTAccount:  true,
		FeatureSettings:    map[string]bool{"cli_daybreak": true},
		DaybreakDefault:    true,
		ModelPickerOptions: catalog,
		OnUpdateDaybreak: func(threadID string, enabled bool) error {
			if threadID != "thread-daybreak" {
				t.Fatalf("persisted for thread %q", threadID)
			}
			updates = append(updates, enabled)
			if persist != nil {
				return persist(threadID, enabled)
			}
			return nil
		},
	})
	if !model.daybreakEnabled {
		t.Fatal("Daybreak should start enabled for this fixture")
	}
	return model, &updates
}

// daybreakHistoryText concatenates the raw transcript text so assertions are not
// clipped by the terminal width.
func daybreakHistoryText(model *Model) string {
	if model == nil || model.State == nil {
		return ""
	}
	parts := make([]string, 0, len(model.State.Messages))
	for _, message := range model.State.Messages {
		parts = append(parts, message.RawText)
	}
	return strings.Join(parts, "\n")
}

func daybreakCatalog() []codextui.ModelPickerOption {
	return []codextui.ModelPickerOption{
		{ID: "gpt-5", AvailableCyberPrograms: []string{"standard"}},
		{ID: "gpt-6", AvailableCyberPrograms: []string{"standard", "daybreak_blue"}},
	}
}

// TestDaybreakDisabledWhenSelectedModelLacksAccess pins Rust #60526: switching
// to a model whose explicit catalog metadata excludes Daybreak turns it off for
// the thread, persists that choice and warns about earlier cyber context.
func TestDaybreakDisabledWhenSelectedModelLacksAccess(t *testing.T) {
	model, updates := daybreakAccessModel(t, "gpt-6", daybreakCatalog(), nil)
	model.Update(StatusMsg{Status: ""})
	if !model.daybreakEnabled || len(*updates) != 0 {
		t.Fatalf("supported model changed Daybreak: enabled=%v updates=%v", model.daybreakEnabled, *updates)
	}

	model.State.Model = "gpt-5"
	model.Update(StatusMsg{Status: ""})
	if model.daybreakEnabled {
		t.Fatal("Daybreak stayed on for a model without Daybreak access")
	}
	if len(*updates) != 1 || (*updates)[0] {
		t.Fatalf("persisted updates = %v, want one disable", *updates)
	}
	history := daybreakHistoryText(model)
	for _, want := range []string{
		"Daybreak has been turned off for new turns because it is not available for gpt-5",
		"Prior cybersecurity context in this thread may cause a refusal.",
		"/daybreak to re-enable it",
	} {
		if !strings.Contains(history, want) {
			t.Fatalf("warning missing %q:\n%s", want, history)
		}
	}
}

// TestDaybreakKeptOnWhenCatalogLacksAccessMetadata pins Rust #60526's explicit
// metadata requirement: a catalog entry without access programs proves nothing.
func TestDaybreakKeptOnWhenCatalogLacksAccessMetadata(t *testing.T) {
	model, updates := daybreakAccessModel(t, "gpt-5", []codextui.ModelPickerOption{{ID: "gpt-5"}}, nil)
	model.Update(StatusMsg{Status: ""})
	if !model.daybreakEnabled || len(*updates) != 0 {
		t.Fatalf("unknown metadata changed Daybreak: enabled=%v updates=%v", model.daybreakEnabled, *updates)
	}
}

// TestDaybreakDisableFollowsCatalogRefresh pins that a refreshed catalog can
// reveal the exclusion for the model that is already selected.
func TestDaybreakDisableFollowsCatalogRefresh(t *testing.T) {
	model, updates := daybreakAccessModel(t, "gpt-5", nil, nil)
	model.Update(StatusMsg{Status: ""})
	if !model.daybreakEnabled {
		t.Fatal("an empty catalog should leave Daybreak alone")
	}

	model.modelPickerOpts = daybreakCatalog()
	model.Update(StatusMsg{Status: ""})
	if model.daybreakEnabled || len(*updates) != 1 {
		t.Fatalf("catalog refresh did not disable Daybreak: enabled=%v updates=%v", model.daybreakEnabled, *updates)
	}
}

// TestDaybreakStaysOnWhenDisableCannotPersist pins Rust's failure path: the
// preference is only applied once the thread write succeeded.
func TestDaybreakStaysOnWhenDisableCannotPersist(t *testing.T) {
	model, updates := daybreakAccessModel(t, "gpt-5", daybreakCatalog(), func(string, bool) error {
		return errors.New("thread write failed")
	})
	model.Update(StatusMsg{Status: ""})
	if !model.daybreakEnabled {
		t.Fatal("Daybreak was disabled although persisting failed")
	}
	if len(*updates) != 1 {
		t.Fatalf("persisted updates = %v, want one failed attempt", *updates)
	}
	if strings.Contains(daybreakHistoryText(model), "has been turned off for new turns because") {
		t.Fatal("a failed persist still warned that Daybreak was turned off")
	}
}

// TestDaybreakStaysOffAfterSwitchingBack pins Rust #60526: a compatible model
// does not silently re-enable Daybreak; /daybreak remains the way back.
func TestDaybreakStaysOffAfterSwitchingBack(t *testing.T) {
	model, updates := daybreakAccessModel(t, "gpt-5", daybreakCatalog(), nil)
	model.Update(StatusMsg{Status: ""})
	model.State.Model = "gpt-6"
	model.Update(StatusMsg{Status: ""})
	if model.daybreakEnabled {
		t.Fatal("switching back re-enabled Daybreak")
	}
	if len(*updates) != 1 {
		t.Fatalf("persisted updates = %v, want only the disable", *updates)
	}
}
