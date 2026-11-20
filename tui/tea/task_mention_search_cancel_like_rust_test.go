package tea

import (
	"context"
	"testing"
	"time"

	codextui "codex_go/tui"
)

// Rust #62132: a new query cancels the task search it replaces, so a
// superseded in-flight search stops instead of finishing for a popup that no
// longer shows it.
func TestModelTaskMentionSearchCancelsSupersededQueryLikeRust(t *testing.T) {
	started := make(chan context.Context, 1)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-1")
	model := NewModel(state, Options{
		OnSearchTasks: func(ctx context.Context, query string, currentThreadID string, cwd string) ([]codextui.TaskMention, error) {
			started <- ctx
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	model.Update(TaskToolsAvailableMsg{ThreadID: "thread-1", Available: true})
	model.Update(runes("@"))
	_, first := model.Update(runes("a"))
	done := make(chan struct{})
	go func() {
		defer close(done)
		if first != nil {
			_ = first()
		}
	}()
	var searchCtx context.Context
	select {
	case searchCtx = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("task search never started")
	}
	if searchCtx.Err() != nil {
		t.Fatal("search was cancelled before it was superseded")
	}
	model.Update(runes("ab"))
	select {
	case <-searchCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("superseded task search was not cancelled")
	}
	<-done
}
