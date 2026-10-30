package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func newSessionCheckoutModel(t *testing.T, options Options) *Model {
	t.Helper()
	if options.WorktreeRepositoryAvailable == nil {
		options.WorktreeRepositoryAvailable = func(string) bool { return true }
	}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	state.CWD = `D:\repo`
	return NewModel(state, options)
}

func checkoutPickerOptions(model *Model) []ModalOption {
	if model == nil || model.modal == nil {
		return nil
	}
	return model.modal.options
}

// TestNewCommandShowsCheckoutPickerLikeRust covers Rust #43120/#50416: `/new`
// asks where the conversation should run, and choosing the current worktree
// starts the named session immediately.
func TestNewCommandShowsCheckoutPickerLikeRust(t *testing.T) {
	model := newSessionCheckoutModel(t, Options{
		WorktreesEnabled:        true,
		LocalWorktreeOperations: true,
	})

	typeText(t, model, "/new my thread")
	if _, cmd := model.Update(key(bubbletea.KeyEnter)); cmd != nil {
		t.Fatalf("/new should wait for a checkout choice, got %v", cmd())
	}
	if model.modal == nil || model.modal.kind != ModalKindPicker || model.modal.id != sessionCheckoutModalID {
		t.Fatalf("checkout picker not open: %#v", model.modal)
	}
	if model.modal.title != "Where should the new conversation run?" {
		t.Fatalf("title = %q", model.modal.title)
	}
	options := checkoutPickerOptions(model)
	if len(options) != 2 {
		t.Fatalf("options = %#v", options)
	}
	if options[0].Label != "Use current Git worktree" ||
		options[0].Description != "Keep using the current working directory" {
		t.Fatalf("current option = %#v", options[0])
	}
	if options[1].Label != "Create new Git worktree" ||
		options[1].Description != "Create a separate checkout of this repository in another directory" {
		t.Fatalf("create option = %#v", options[1])
	}

	model.Update(key(bubbletea.KeyEnter))
	if model.modal != nil {
		t.Fatalf("choosing the current worktree must dismiss the picker: %#v", model.modal)
	}
	if model.notice != "Started a new session named my thread." {
		t.Fatalf("notice = %q", model.notice)
	}
}

// TestForkCommandCheckoutPickerCreatesNamedWorktreeLikeRust covers the
// creation branch: the requested fork name reaches the app handler.
func TestForkCommandCheckoutPickerCreatesNamedWorktreeLikeRust(t *testing.T) {
	type startCall struct {
		mode string
		name string
		cwd  string
	}
	var calls []startCall
	model := newSessionCheckoutModel(t, Options{
		WorktreesEnabled:        true,
		LocalWorktreeOperations: true,
		OnStartManagedWorktree: func(mode string, name string, cwd string, threadID string) (SessionResumeResponse, error) {
			calls = append(calls, startCall{mode: mode, name: name, cwd: cwd})
			return SessionResumeResponse{
				Summary: &codextui.SessionSummary{ThreadID: "thread-worktree", CWD: `D:\repo\wt`},
			}, nil
		},
	})

	typeText(t, model, "/fork named-fork")
	if _, cmd := model.Update(key(bubbletea.KeyEnter)); cmd != nil {
		t.Fatalf("/fork should wait for a checkout choice, got %v", cmd())
	}
	if model.modal == nil || model.modal.title != "Where should the forked conversation run?" {
		t.Fatalf("checkout picker not open: %#v", model.modal)
	}

	model.Update(key(bubbletea.KeyDown))
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(calls) != 1 || calls[0].mode != managedWorktreeModeFork || calls[0].name != "named-fork" {
		t.Fatalf("worktree creation calls = %#v", calls)
	}
	if model.State.ThreadID != "thread-worktree" {
		t.Fatalf("thread = %q, want the created session", model.State.ThreadID)
	}
}

// TestCheckoutPickerFallsBackWhenWorktreesUnavailable keeps the direct command
// behavior when managed worktrees cannot run.
func TestCheckoutPickerFallsBackWhenWorktreesUnavailable(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	state := model.State
	state.SetThreadID("thread-main")
	state.CWD = `D:\repo`

	typeText(t, model, "/new direct")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal != nil {
		t.Fatalf("unavailable worktrees must not open the picker: %#v", model.modal)
	}
	if model.notice != "Started a new session named direct." {
		t.Fatalf("notice = %q", model.notice)
	}
}
