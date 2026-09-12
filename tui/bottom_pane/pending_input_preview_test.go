package bottompane

import (
	"strings"
	"testing"
)

// TestPendingInputPreviewKeepsQueuedGroupWithQuestions covers Rust #42903: while
// async questions are pending the queued follow-up group stays visible, keeps
// its header even when empty, and yields the edit hint to the question summary.
func TestPendingInputPreviewKeepsQueuedGroupWithQuestions(t *testing.T) {
	preview := NewPendingInputPreview()
	preview.HasQuestions = true
	preview.QueuedMessages = []string{"queued follow-up"}
	joined := strings.Join(preview.RenderLines(60), "\n")
	if !strings.Contains(joined, "Queued follow-up inputs") || !strings.Contains(joined, "queued follow-up") {
		t.Fatalf("queued group missing:\n%s", joined)
	}
	if strings.Contains(joined, "edit last queued message") {
		t.Fatalf("edit hint must yield to the question summary:\n%s", joined)
	}

	empty := NewPendingInputPreview()
	empty.HasQuestions = true
	lines := empty.RenderLines(60)
	if len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "Queued follow-up inputs") {
		t.Fatalf("empty queued group should stay visible with questions: %#v", lines)
	}

	noQuestions := NewPendingInputPreview()
	noQuestions.QueuedMessages = []string{"queued follow-up"}
	if !strings.Contains(strings.Join(noQuestions.RenderLines(60), "\n"), "edit last queued message") {
		t.Fatal("without questions the edit hint must render")
	}
}
