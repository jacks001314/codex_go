package tea

import (
	"reflect"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// runModelCmd executes one command (unwrapping batch commands) and feeds its
// messages back into the model, mirroring the Bubble Tea runtime.
func runModelCmd(model *Model, cmd bubbletea.Cmd) int {
	if cmd == nil {
		return 0
	}
	switch message := cmd().(type) {
	case nil:
		return 0
	case bubbletea.BatchMsg:
		count := 0
		for _, child := range message {
			count += runModelCmd(model, child)
		}
		return count
	default:
		model.Update(message)
		return 1
	}
}

// TestAPIModelCatalogRefreshOnAttachedCWDLikeRust covers #55682: attaching to a
// session in another project refreshes an API-key model catalog from that
// project instead of keeping the launch directory's access information.
func TestAPIModelCatalogRefreshOnAttachedCWDLikeRust(t *testing.T) {
	launch := t.TempDir()
	project := t.TempDir()
	var requested []string
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	model := NewModel(state, Options{
		Width:            100,
		Height:           30,
		SessionPickerCWD: launch,
		OnListModels: func(cwd string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
			requested = append(requested, cwd)
			return []codextui.ModelPickerOption{{ID: "project-model", Label: "Project"}}, nil
		},
	})

	cmd := model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{CWD: project},
	})
	if cmd == nil {
		t.Fatal("attached cwd change did not request a model catalog refresh")
	}
	runModelCmd(model, cmd)
	if !reflect.DeepEqual(requested, []string{project}) {
		t.Fatalf("catalog cwd requests = %#v, want %q", requested, project)
	}
	if len(model.modelCatalogOpts) != 1 || model.modelCatalogOpts[0].ID != "project-model" {
		t.Fatalf("model catalog = %#v", model.modelCatalogOpts)
	}
}

// TestChatGPTModelCatalogStaysOnCWDChangeLikeRust pins the auth gate: only an
// API-key session refreshes account-scoped models when the project changes.
func TestChatGPTModelCatalogStaysOnCWDChangeLikeRust(t *testing.T) {
	launch := t.TempDir()
	project := t.TempDir()
	requests := 0
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	model := NewModel(state, Options{
		Width:             100,
		Height:            30,
		SessionPickerCWD:  launch,
		HasChatGPTAccount: true,
		OnListModels: func(cwd string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
			requests++
			return []codextui.ModelPickerOption{{ID: "chatgpt-model", Label: "ChatGPT"}}, nil
		},
	})

	if cmd := model.applyThreadSettingsUpdated(ThreadSettingsUpdatedMsg{
		ThreadID: "thread-a",
		Settings: appserver.Settings{CWD: project},
	}); cmd != nil {
		t.Fatalf("ChatGPT session unexpectedly refreshed the catalog (cmd=%T)", cmd())
	}
	if requests != 0 {
		t.Fatalf("catalog requests = %d, want none", requests)
	}
}

// TestModelPickerFetchUsesSessionCWDLikeRust ensures every catalog read is
// scoped to the attached session rather than the launch directory.
func TestModelPickerFetchUsesSessionCWDLikeRust(t *testing.T) {
	project := t.TempDir()
	var requested string
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnListModels: func(cwd string, includeHidden bool) ([]codextui.ModelPickerOption, error) {
			requested = cwd
			return []codextui.ModelPickerOption{{ID: "project-model", Label: "Project"}}, nil
		},
	})
	model.sessionCWD = project
	cmd := model.fetchModelsForPicker()
	if cmd == nil {
		t.Fatal("fetchModelsForPicker returned nil")
	}
	if _, ok := cmd().(ModelsResultMsg); !ok {
		t.Fatal("fetch did not produce a model result")
	}
	if requested != project {
		t.Fatalf("catalog cwd = %q, want %q", requested, project)
	}
}
