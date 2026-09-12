package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func workingDirectoryChangeModel(t *testing.T, local bool, change func(threadID string, cwd string) (*codextui.SessionSummary, error)) *Model {
	t.Helper()
	source := t.TempDir()
	model := NewModel(codextui.NewState(nil), Options{
		Width:                    120,
		Height:                   40,
		LocalSession:             local,
		OnWorkingDirectoryChange: change,
	})
	model.State.SetThreadID("thread-a")
	model.State.CWD = source
	return model
}

// TestWorkingDirectoryChangeRejectsConcurrentRequests covers Rust #43376: a
// second /cd is refused while the first is pending, and the recheck drops a
// result whose source session changed.
func TestWorkingDirectoryChangeRejectsConcurrentRequests(t *testing.T) {
	calls := 0
	source := filepath.Join(t.TempDir(), "source")
	destination := filepath.Join(t.TempDir(), "destination")
	for _, dir := range []string{source, destination} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	model := workingDirectoryChangeModel(t, true, func(threadID string, cwd string) (*codextui.SessionSummary, error) {
		calls++
		return &codextui.SessionSummary{ThreadID: "thread-b", CWD: cwd}, nil
	})
	model.State.CWD = source
	cmd := model.applyWorkingDirectoryChangeCommand(destination)
	if cmd == nil {
		t.Fatal("the first /cd should be accepted")
	}
	if !model.workingDirectoryChangePending {
		t.Fatal("the request must be tracked as pending")
	}
	if second := model.applyWorkingDirectoryChangeCommand(source); second != nil {
		t.Fatal("a concurrent /cd must be rejected")
	}
	if !strings.Contains(modelMessageText(model), "Changing directories requires an idle primary session without queued input.") {
		t.Fatalf("concurrent message missing: %q", modelMessageText(model))
	}

	// The deferred result applies when the source session is unchanged.
	updated, _ := model.Update(cmd())
	model = updated.(*Model)
	if model.workingDirectoryChangePending {
		t.Fatal("the pending flag must clear when the change finishes")
	}
	if model.State.ThreadID != "thread-b" || model.State.CWD != destination {
		t.Fatalf("state = (%q, %q)", model.State.ThreadID, model.State.CWD)
	}
	if calls != 1 {
		t.Fatalf("change calls = %d", calls)
	}
}

// TestWorkingDirectoryChangeRecheckDropsStaleResults covers the deferred
// source recheck (Rust #43376): a result whose source thread or directory is no
// longer current is discarded.
func TestWorkingDirectoryChangeRecheckDropsStaleResults(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	destination := filepath.Join(t.TempDir(), "destination")
	for _, dir := range []string{source, destination} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	model := workingDirectoryChangeModel(t, true, func(threadID string, cwd string) (*codextui.SessionSummary, error) {
		return &codextui.SessionSummary{ThreadID: "thread-b", CWD: cwd}, nil
	})
	model.State.CWD = source
	cmd := model.applyWorkingDirectoryChangeCommand(destination)
	if cmd == nil {
		t.Fatal("the /cd should be accepted")
	}
	// The session switches threads before the change finishes.
	model.State.SetThreadID("thread-elsewhere")
	updated, _ := model.Update(cmd())
	model = updated.(*Model)
	if model.State.ThreadID != "thread-elsewhere" || model.State.CWD != source {
		t.Fatalf("stale change applied: (%q, %q)", model.State.ThreadID, model.State.CWD)
	}
	if !strings.Contains(modelMessageText(model), "Changing directories requires an idle primary session without queued input.") {
		t.Fatalf("stale message missing: %q", modelMessageText(model))
	}

	// A changed source directory is also dropped.
	model = workingDirectoryChangeModel(t, true, func(threadID string, cwd string) (*codextui.SessionSummary, error) {
		return &codextui.SessionSummary{ThreadID: "thread-b", CWD: cwd}, nil
	})
	model.State.CWD = source
	cmd = model.applyWorkingDirectoryChangeCommand(destination)
	model.State.CWD = filepath.Join(t.TempDir(), "moved")
	updated, _ = model.Update(cmd())
	model = updated.(*Model)
	if model.State.CWD == destination {
		t.Fatalf("stale directory change applied: %q", model.State.CWD)
	}
}

// TestWorkingDirectoryChangeRemoteMessage covers Rust #43376's remote message:
// remote workspaces and execution environments have no local directory to
// change.
func TestWorkingDirectoryChangeRemoteMessage(t *testing.T) {
	model := workingDirectoryChangeModel(t, false, nil)
	model.applyWorkingDirectoryChangeCommand(filepath.Join(t.TempDir(), "other"))
	if !strings.Contains(modelMessageText(model), "Changing directories is not supported for remote workspaces or remote execution environments.") {
		t.Fatalf("remote message missing: %q", modelMessageText(model))
	}
}
