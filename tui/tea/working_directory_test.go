package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	codextui "codex_go/tui"

	bubbletea "github.com/charmbracelet/bubbletea"
)

func TestModelPwdDisplaysCurrentWorkingDirectory(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.CWD = t.TempDir()
	model := NewModel(state, Options{})
	model.onWorkingDirectoryChange = func(threadID string, cwd string) (*codextui.SessionSummary, error) {
		t.Fatalf("onWorkingDirectoryChange must not run for /pwd")
		return nil, nil
	}

	typeText(t, model, "/pwd")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)

	texts := state.Messages
	found := false
	for _, message := range texts {
		if strings.Contains(message.RawText, "Current working directory: "+state.CWD) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("transcript missing pwd display: %#v", state.Messages)
	}
}

func TestModelCdChangesWorkingDirectoryPreservingHistory(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	sourceCWD := t.TempDir()
	targetCWD := filepath.Join(sourceCWD, "target")
	if err := os.MkdirAll(targetCWD, 0o755); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	state.CWD = sourceCWD
	var requests []string
	model := NewModel(state, Options{
		OnWorkingDirectoryChange: func(threadID string, cwd string) (*codextui.SessionSummary, error) {
			requests = append(requests, threadID+":"+cwd)
			return &codextui.SessionSummary{
				ThreadID: "thread-replaced",
				Title:    "Replaced",
				CWD:      cwd,
			}, nil
		},
	})

	typeText(t, model, "/cd "+targetCWD)
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)

	if len(requests) != 1 || requests[0] != "thread-source:"+targetCWD {
		t.Fatalf("working directory change requests = %#v", requests)
	}
	if state.ThreadID != "thread-replaced" {
		t.Fatalf("ThreadID = %q, want thread-replaced", state.ThreadID)
	}
	if state.CWD != targetCWD {
		t.Fatalf("CWD = %q, want %q", state.CWD, targetCWD)
	}
}

func TestModelCdRejectsRunningSession(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.CWD = t.TempDir()
	state.Status = "running"
	called := false
	model := NewModel(state, Options{
		OnWorkingDirectoryChange: func(threadID string, cwd string) (*codextui.SessionSummary, error) {
			called = true
			return nil, nil
		},
	})

	typeText(t, model, "/cd /tmp")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if called {
		t.Fatal("working directory change ran for a running session")
	}
}

func TestModelCdDefaultsToHomeAndExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	state.CWD = t.TempDir()
	var requests []string
	model := NewModel(state, Options{
		OnWorkingDirectoryChange: func(threadID string, cwd string) (*codextui.SessionSummary, error) {
			requests = append(requests, cwd)
			return &codextui.SessionSummary{ThreadID: "replaced", CWD: cwd}, nil
		},
	})

	typeText(t, model, "/cd")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(requests) != 1 || requests[0] != home {
		t.Fatalf("default cd target = %#v, want home %q", requests, home)
	}
}
