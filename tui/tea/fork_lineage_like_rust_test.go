package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #61183: a resumed forked conversation displays its own parent, and the
// cached title follows that parent instead of one inherited from another
// thread's session state.
func TestResumeShowsTargetForkLineageLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-parent")

	model.applyResumeResponseWithCWD("thread-child", SessionResumeResponse{
		Summary: &codextui.SessionSummary{
			ThreadID:        "thread-child",
			ForkedFromID:    "thread-parent",
			ForkParentTitle: "Parent chat",
		},
	}, "/repo")
	if content := model.transcriptContent(); !strings.Contains(content, "Thread forked from Parent chat (thread-parent)") {
		t.Fatalf("fork lineage missing: %q", content)
	}

	// Resuming a thread with no parent clears the inherited line because the
	// transcript is rebuilt from that thread's summary.
	model.applyResumeResponseWithCWD("thread-solo", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-solo"},
	}, "/repo")
	if content := model.transcriptContent(); strings.Contains(content, "Thread forked from") {
		t.Fatalf("lineage leaked into an unrelated thread: %q", content)
	}

	// An unreadable parent still shows the fork without a title.
	model.applyResumeResponseWithCWD("thread-child-2", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-child-2", ForkedFromID: "thread-missing"},
	}, "/repo")
	if content := model.transcriptContent(); !strings.Contains(content, "Thread forked from thread-missing") {
		t.Fatalf("id-only lineage missing: %q", content)
	}
}
