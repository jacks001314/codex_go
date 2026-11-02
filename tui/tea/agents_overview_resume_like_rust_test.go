package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// resumePickerTestModel opens the command center with a resume picker item and
// one current task, mirroring the state Rust #42419 resumes from.
func resumePickerTestModel(t *testing.T, resume SessionResumeFunc) *Model {
	t.Helper()
	model := NewModel(nil, Options{
		Width:            120,
		Height:           24,
		SessionPickerCWD: `D:\repo`,
		SessionPickerItems: []codextui.SessionSummary{
			{ThreadID: "thread-new", Title: "Newer Session", CWD: `D:\repo`, Provider: "openai"},
		},
		OnResumeSession: resume,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	if model.State != nil {
		model.State.ThreadID = "root-1"
	}
	model.agentsOverview.Selected = 0
	return model
}

func resumeThreadSelection(threadID string) codextui.SessionSelection {
	return codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{ThreadID: threadID, CWD: `D:\repo`},
	}
}

// Rust #42419: a resume failure stays inside the command center, and a picker
// that cannot open shows "Unable to resume session" with a return item.
func TestAgentsOverviewResumePickerErrorLikeRust(t *testing.T) {
	model := resumePickerTestModel(t, func(codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{}, errors.New("boom")
	})
	decision, notice, complete := model.applyResumePickerSelection(resumeThreadSelection("thread-new"))
	if decision != nil || !complete || !strings.Contains(notice, "Resume failed: boom") {
		t.Fatalf("failed resume = %#v %q %v", decision, notice, complete)
	}
	if model.agentsOverview == nil {
		t.Fatal("a failed resume closed the command center")
	}
	model.addSessionPickerError("Failed to open session picker: boom")
	if model.modal == nil || model.modal.title != "Unable to resume session" {
		t.Fatalf("picker error modal = %#v", model.modal)
	}
	if model.modal.body != "Failed to open session picker: boom" {
		t.Fatalf("picker error body = %q", model.modal.body)
	}
	if len(model.modal.options) != 1 || model.modal.options[0].Label != "Return to command center" {
		t.Fatalf("picker error options = %#v", model.modal.options)
	}
	// Selecting the only item dismisses the view back to the task list.
	updated, _ := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if model.modal != nil {
		t.Fatal("return item did not close the picker error view")
	}
	if model.agentsOverview == nil {
		t.Fatal("return item left the command center")
	}
}

// Rust #42419: switching tasks through the resume picker keeps each task's
// composer draft and restores the resumed task's draft.
func TestAgentsOverviewResumePickerRestoresDraftLikeRust(t *testing.T) {
	model := resumePickerTestModel(t, func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{
			Status:  "idle",
			Summary: &codextui.SessionSummary{ThreadID: selection.Target.ThreadID, Title: "Newer Session", CWD: `D:\repo`},
		}, nil
	})
	model.setComposerPrompt("draft for root-1")
	model.agentsOverviewDrafts["thread-new"] = "unsent for thread-new"
	decision, _, complete := model.applyResumePickerSelection(resumeThreadSelection("thread-new"))
	if decision == nil || !complete {
		t.Fatalf("resume selection = %#v %v", decision, complete)
	}
	if model.agentsOverview == nil {
		t.Fatal("resuming an idle task closed the command center")
	}
	if got := model.agentsOverviewDrafts["root-1"]; got != "draft for root-1" {
		t.Fatalf("root-1 draft = %q", got)
	}
	if got := model.composerText(); got != "unsent for thread-new" {
		t.Fatalf("restored composer = %q", got)
	}
	if _, kept := model.agentsOverviewDrafts["thread-new"]; kept {
		t.Fatal("the restored draft was not consumed")
	}
}

// Rust #42419: resuming a task whose turn is still running returns to the
// conversation instead of leaving the command center open.
func TestAgentsOverviewResumePickerActiveTaskClosesLikeRust(t *testing.T) {
	model := resumePickerTestModel(t, func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
		return SessionResumeResponse{
			Status:  "running",
			Summary: &codextui.SessionSummary{ThreadID: selection.Target.ThreadID, Title: "Newer Session", CWD: `D:\repo`},
		}, nil
	})
	if _, _, complete := model.applyResumePickerSelection(resumeThreadSelection("thread-new")); !complete {
		t.Fatal("active resume did not complete")
	}
	if model.agentsOverview != nil {
		t.Fatal("resuming an active task kept the command center open")
	}
}
