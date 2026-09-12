package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

func readOnlyResumeModel(t *testing.T, responses []SessionResumeResponse) (*Model, *int) {
	t.Helper()
	calls := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:  120,
		Height: 40,
		OnResumeSession: func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
			index := calls
			calls++
			if index < len(responses) {
				return responses[index], nil
			}
			return SessionResumeResponse{}, nil
		},
	})
	return model, &calls
}

// TestReadOnlyResumeShowsNoticeAndBlocksInput covers Rust #43253: a resume that
// falls back to an external writer renders the notice instead of the composer,
// ignores typing, and retries on R.
func TestReadOnlyResumeShowsNoticeAndBlocksInput(t *testing.T) {
	model, calls := readOnlyResumeModel(t, []SessionResumeResponse{
		{
			Summary:        &codextui.SessionSummary{ThreadID: "thread-a", Title: "Owned elsewhere"},
			Messages:       []codextui.Message{{Role: codextui.RoleAssistant, Text: "history"}},
			ThreadSettings: &appserver.Settings{Model: "server-model", ApprovalPolicy: "on-request", SandboxPolicy: "workspace-write"},
		},
	})
	model.State.SetThreadID("thread-a")
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a", Title: "Owned elsewhere"},
		Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "history"}},
		ReadOnly: true,
	})
	if !model.readOnlyThread {
		t.Fatal("a read-only resume must enter the external-writer state")
	}
	view := model.View()
	if !strings.Contains(view, "This conversation is open in another app") || !strings.Contains(view, "R to Retry") {
		t.Fatalf("read-only notice missing:\n%s", view)
	}
	if strings.Contains(view, "Type your message") {
		t.Fatalf("composer must be replaced by the notice:\n%s", view)
	}

	// Typing is ignored and submissions are blocked.
	model.Update(keyRunes('h'))
	model.Update(keyRunes('i'))
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer = %q, read-only input must be blocked", got)
	}
	if _, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter}); cmd != nil {
		t.Fatal("read-only submit must be blocked")
	}
	if len(model.SubmittedRequests()) != 0 {
		t.Fatalf("read-only submissions = %#v", model.SubmittedRequests())
	}

	// R retries the resume; the second response clears the read-only state and
	// applies the thread's server settings.
	updated, _ := model.Update(keyRunes('r'))
	model = updated.(*Model)
	if *calls != 1 {
		t.Fatalf("retry calls = %d, want 1", *calls)
	}
	if model.readOnlyThread {
		t.Fatal("a successful retry must clear the read-only state")
	}
	if model.State.Model != "server-model" || model.State.ApprovalPolicy != "on-request" || model.State.Sandbox != "workspace-write" {
		t.Fatalf("resumed settings = (%q, %q, %q)", model.State.Model, model.State.ApprovalPolicy, model.State.Sandbox)
	}
	if !strings.Contains(model.View(), "Type your message") && !strings.Contains(model.View(), "history") {
		t.Fatalf("transcript missing after retry:\n%s", model.View())
	}
}

// TestReadOnlyResumeKeysAdvertised pins the notice's advertised keys: Esc and q
// exit, the transcript shortcut stays available.
func TestReadOnlyResumeKeysAdvertised(t *testing.T) {
	model, _ := readOnlyResumeModel(t, nil)
	model.State.SetThreadID("thread-a")
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a"},
		ReadOnly: true,
	})
	if _, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc}); cmd == nil {
		t.Fatal("Esc must exit the read-only view")
	}
	if _, cmd := model.Update(keyRunes('q')); cmd == nil {
		t.Fatal("q must exit the read-only view")
	}
	if _, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlT}); cmd == nil {
		t.Fatal("the transcript shortcut must stay available")
	}
}
