package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/config"
	codextui "codex_go/tui"
)

// Rust parity: #43113 (fc748ab8d5). Both opt-in prompts share one view, the
// confirmation writes through the app server for new threads only, and memories
// also write the legacy features.memory_tool key.
func TestFeatureEnablePromptSavesMemoriesForNewThreadsLikeRust(t *testing.T) {
	var writes [][]SettingsEdit
	model := NewModel(codextui.NewState(nil), Options{
		Width:  90,
		Height: 24,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{FeatureSettings: map[string]bool{"memories": true, "memory_tool": true}}, nil
		},
	})

	model.openFeatureEnablePrompt("memories")
	if model.modal == nil || model.modal.title != "Enable memories?" {
		t.Fatalf("prompt = %#v", model.modal)
	}
	if model.modal.body != "Memories are disabled in this TUI session." {
		t.Fatalf("prompt body = %q", model.modal.body)
	}
	view := model.View()
	for _, want := range []string{"Save on the server for new threads without changing this thread", memoriesDocsURL, "Not now"} {
		if !strings.Contains(view, want) {
			t.Fatalf("prompt missing %q:\n%s", want, view)
		}
	}

	_, command := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, command)
	if len(writes) != 1 || len(writes[0]) != 2 {
		t.Fatalf("writes = %#v, want both memory keys", writes)
	}
	if writes[0][0].KeyPath != "features.memories" || writes[0][1].KeyPath != "features.memory_tool" {
		t.Fatalf("edit keys = %#v", writes[0])
	}
	if writes[0][0].Value != true || writes[0][1].Value != true {
		t.Fatalf("edit values = %#v", writes[0])
	}
	// Rust keeps the running task unchanged: only new threads see the opt-in.
	if model.featureSettings["memories"] || model.featureSettings["memory_tool"] {
		t.Fatal("the current thread's feature settings changed")
	}
	if !strings.Contains(model.View(), "Memories setting saved on the server for new threads. This thread is unchanged.") {
		t.Fatalf("save notice missing:\n%s", model.View())
	}
}

func TestFeatureEnablePromptSubagentsLikeRust(t *testing.T) {
	var writes [][]SettingsEdit
	model := NewModel(codextui.NewState(nil), Options{
		Width:  90,
		Height: 24,
		OnWriteSettings: func(edits []SettingsEdit) (SettingsWriteResult, error) {
			writes = append(writes, append([]SettingsEdit(nil), edits...))
			return SettingsWriteResult{}, nil
		},
	})

	model.openFeatureEnablePrompt("multi_agent")
	if model.modal == nil || model.modal.title != "Enable subagents?" || model.modal.body != "Subagents are disabled in this TUI session." {
		t.Fatalf("prompt = %#v", model.modal)
	}
	_, command := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, command)
	if len(writes) != 1 || len(writes[0]) != 1 || writes[0][0].KeyPath != "features.multi_agent" {
		t.Fatalf("writes = %#v", writes)
	}
	if !strings.Contains(model.View(), "Subagents setting saved on the server for new threads.") {
		t.Fatalf("save notice missing:\n%s", model.View())
	}
}

func TestFeatureEnablePromptReportsOverrideAndFailureLikeRust(t *testing.T) {
	overridden := NewModel(codextui.NewState(nil), Options{
		Width:  90,
		Height: 24,
		OnWriteSettings: func([]SettingsEdit) (SettingsWriteResult, error) {
			return SettingsWriteResult{WriteStatus: config.WriteOKOverridden}, nil
		},
	})
	overridden.openFeatureEnablePrompt("multi_agent")
	_, command := overridden.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, overridden, command)
	if !strings.Contains(overridden.View(), "Subagents setting was saved but is overridden") {
		t.Fatalf("override notice missing:\n%s", overridden.View())
	}

	failed := NewModel(codextui.NewState(nil), Options{
		Width:  90,
		Height: 24,
		OnWriteSettings: func([]SettingsEdit) (SettingsWriteResult, error) {
			return SettingsWriteResult{}, errors.New("config.toml is read-only")
		},
	})
	failed.openFeatureEnablePrompt("multi_agent")
	_, command = failed.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, failed, command)
	if !strings.Contains(failed.View(), "Failed to save Subagents setting: config.toml is read-only") {
		t.Fatalf("failure notice missing:\n%s", failed.View())
	}
}
