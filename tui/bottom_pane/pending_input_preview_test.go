package bottompane

import (
	"strings"
	"testing"

	"codex_go/tui"
)

// TestPendingInputPreviewLinksURLsLikeRust mirrors Rust #51471: pending steers,
// rejected steers, and queued follow-ups render web URLs as terminal hyperlinks
// with their complete destination.
func TestPendingInputPreviewLinksURLsLikeRust(t *testing.T) {
	url := "https://example.com/docs/guide?page=2"
	preview := NewPendingInputPreview()
	preview.PendingSteers = []string{"see " + url}
	preview.RejectedSteers = []string{"retry " + url}
	preview.QueuedMessages = []string{"queued " + url}
	lines := preview.RenderLines(48)
	linked := 0
	visible := strings.Builder{}
	for _, line := range lines {
		if strings.Contains(line, tui.OSC8Hyperlink(url, url)) {
			linked++
		}
		visible.WriteString(tui.StripOSC8(line))
		visible.WriteString("\n")
	}
	if linked != 3 {
		t.Fatalf("expected three complete hyperlinks, got %d:\n%s", linked, strings.Join(lines, "\n"))
	}
	if !strings.Contains(visible.String(), url) {
		t.Fatalf("visible text lost the URL:\n%s", visible.String())
	}
}

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
