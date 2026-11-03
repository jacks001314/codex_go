package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// Rust #42428: typing in the command center composes a task in the shared
// composer, and Enter starts a session in the selected checkout and sends the
// prompt as its first turn.
func TestAgentsOverviewComposerDispatchLikeRust(t *testing.T) {
	var startedCWD string
	var submitted string
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnAgentsOverviewNewSession: func(cwd string) (AgentThreadSwitchResponse, error) {
			startedCWD = cwd
			return AgentThreadSwitchResponse{Entry: codextui.AgentThreadEntry{ThreadID: "task-1"}}, nil
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = request.Prompt
			return nil
		},
	})
	openAgentsDashboard(t, model)
	model.agentsOverview.Selected = 1 // beta lives in /work/a
	typeText(t, model, "Build the parser")
	if !model.agentsOverview.Composing() {
		t.Fatal("typing did not focus the shared composer")
	}
	if got := model.agentsOverview.ComposerText(); got != "Build the parser" {
		t.Fatalf("composer draft = %q", got)
	}
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("enter returned no task-start command")
	}
	updated, _ = model.Update(command())
	model = updated.(*Model)
	if startedCWD != "/work/a" {
		t.Fatalf("task cwd = %q, want /work/a", startedCWD)
	}
	if model.agentsOverview != nil {
		t.Fatal("dispatching a task left the command center open")
	}
	if submitted != "Build the parser" {
		t.Fatalf("submitted prompt = %q", submitted)
	}
}

// Rust #42428: while the dashboard shows a connection or refresh error the
// composer stays editable but Enter inserts a newline instead of dispatching.
func TestAgentsOverviewComposerOfflineBlocksDispatchLikeRust(t *testing.T) {
	started := false
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return nil, errors.New("disconnected")
		},
		OnAgentsOverviewNewSession: func(string) (AgentThreadSwitchResponse, error) {
			started = true
			return AgentThreadSwitchResponse{}, nil
		},
	})
	openAgentsDashboard(t, model)
	typeText(t, model, "Build the parser")
	if !model.agentsOverview.Composing() {
		t.Fatal("offline editing did not focus the composer")
	}
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command != nil {
		_ = command()
	}
	if started {
		t.Fatal("an offline Enter dispatched a task")
	}
	if got := model.agentsOverview.ComposerText(); got != "Build the parser\n" {
		t.Fatalf("offline Enter inserted %q, want a newline", got)
	}
}

// Rust #42428: a dashboard refresh keeps the composed draft instead of
// replacing the composer.
func TestAgentsOverviewComposerKeepsDraftAcrossRefreshLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	typeText(t, model, "Build the parser")
	updated, _ := model.Update(agentsOverviewListMsg{
		rows:      agentsOverviewTestRows(),
		requestID: model.agentsOverviewRefresh,
	})
	model = updated.(*Model)
	if got := model.agentsOverview.ComposerText(); got != "Build the parser" {
		t.Fatalf("draft after refresh = %q", got)
	}
	if !strings.Contains(model.agentsOverview.ComposerText(), "Build") {
		t.Fatal("draft lost")
	}
}

// Rust #42428: a failed session start restores the unsent prompt into the
// composer so the task is not lost.
func TestAgentsOverviewComposerRestoresPromptOnFailureLikeRust(t *testing.T) {
	model := NewModel(nil, Options{
		Width:  120,
		Height: 24,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
		OnAgentsOverviewNewSession: func(string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{}, errors.New("start failed")
		},
	})
	openAgentsDashboard(t, model)
	typeText(t, model, "Build the parser")
	updated, command := model.Update(key(bubbletea.KeyEnter))
	model = updated.(*Model)
	if command == nil {
		t.Fatal("enter returned no task-start command")
	}
	updated, _ = model.Update(command())
	model = updated.(*Model)
	if model.agentsOverview == nil {
		t.Fatal("a failed dispatch closed the command center")
	}
	if got := model.agentsOverview.ComposerText(); got != "Build the parser" {
		t.Fatalf("restored prompt = %q", got)
	}
	if !strings.Contains(model.agentsOverviewNotice, "Failed to start session") {
		t.Fatalf("notice = %q", model.agentsOverviewNotice)
	}
}
