package tea

import (
	"testing"

	codextui "codex_go/tui"
)

func TestDynamicToolThreadStartedTracksDispatchedTasks(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	model := NewModel(state, Options{Width: 80, Height: 24})

	model.Update(DynamicToolThreadStartedMsg{ThreadID: "thread-child", TaskToolsAvailable: true})

	if !model.IsDynamicToolThread("thread-child") {
		t.Fatalf("dispatched task not tracked: %#v", model.dynamicToolThreads)
	}
	if model.IsDynamicToolThread("thread-other") {
		t.Fatal("unrelated thread should not be tracked")
	}
	// A blank thread id is ignored.
	model.Update(DynamicToolThreadStartedMsg{ThreadID: "  "})
	if len(model.dynamicToolThreads) != 1 {
		t.Fatalf("dynamic tool threads = %#v", model.dynamicToolThreads)
	}
}
