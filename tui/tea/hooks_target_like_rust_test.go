package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
)

// TestModelHooksTargetUsesActiveThreadLikeRust mirrors Rust #56553:
// ChatWidget::hooks_list_target asks for the running thread's inventory instead
// of a working-directory listing.
func TestModelHooksTargetUsesActiveThreadLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	var got codextui.HooksListTarget
	model := NewModel(state, Options{
		Width:  80,
		Height: 24,
		OnReadHooks: func(target codextui.HooksListTarget) (appserver.HookListEntry, error) {
			got = target
			return appserver.HookListEntry{CWD: `D:\repo`}, nil
		},
	})
	typeText(t, model, "/hooks")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if !got.IsThread() || got.ThreadID != "thread-1" {
		t.Fatalf("hooks target = %#v, want the active thread", got)
	}
	if model.modal == nil || model.modal.hooksBrowser == nil {
		t.Fatal("hooks browser did not open for a thread target")
	}
}

// TestModelHooksResultDropsStaleTargetLikeRust mirrors Rust on_hooks_loaded:
// a response for a target the chat no longer points at is ignored.
func TestModelHooksResultDropsStaleTargetLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{Width: 80, Height: 24})
	model.applyHooksListResult(HooksListResultMsg{
		Target: codextui.HooksListTargetForThread("thread-0"),
		Entry:  appserver.HookListEntry{CWD: `D:\repo`},
	})
	if model.modal != nil {
		t.Fatalf("stale hooks response opened %#v", model.modal)
	}
	model.applyHooksListResult(HooksListResultMsg{
		Target: codextui.HooksListTargetForThread("thread-1"),
		Entry:  appserver.HookListEntry{CWD: `D:\repo`},
	})
	if model.modal == nil || model.modal.hooksBrowser == nil {
		t.Fatal("current hooks target did not open the browser")
	}
}
