package agentsoverview

import (
	"strings"
	"testing"
)

// Rust #63146: the selected task reports "Opening…" and the footer "Opening
// task…" while its resume request is in flight, then returns to the normal
// status and hints.
func TestAgentCenterOpeningProgressLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	var alpha string
	for _, row := range view.Rows {
		if row.Name == "alpha" {
			alpha = row.ThreadID
		}
	}
	if alpha == "" {
		t.Fatal("sample rows lack alpha")
	}
	view.MarkOpening(alpha)
	if got := view.OpeningThread(); got != alpha {
		t.Fatalf("OpeningThread() = %q, want %q", got, alpha)
	}
	lines := strings.Join(view.Render(120, 20), "\n")
	if !strings.Contains(lines, "Opening\u2026") {
		t.Fatalf("selected row missing Opening status: %s", lines)
	}
	if !strings.Contains(lines, "Opening task\u2026") {
		t.Fatalf("footer missing opening progress: %s", lines)
	}

	// Opening outranks discovery progress in the footer.
	view.Loading = true
	lines = strings.Join(view.Render(120, 20), "\n")
	if !strings.Contains(lines, "Opening task\u2026") {
		t.Fatalf("discovery progress displaced opening: %s", lines)
	}

	view.ClearOpening()
	view.Loading = false
	lines = strings.Join(view.Render(120, 20), "\n")
	if strings.Contains(lines, "Opening task\u2026") {
		t.Fatalf("opening footer survived ClearOpening: %s", lines)
	}
	if !strings.Contains(lines, "Working") {
		t.Fatalf("normal row status not restored: %s", lines)
	}
}
