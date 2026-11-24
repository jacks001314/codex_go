package agentsoverview

import (
	"strings"
	"testing"
)

// Rust #63146: initial discovery reports progress in the footer while the
// already-published tasks stay visible, and the label shortens when the footer
// cannot fit the full text.
func TestAgentCenterFooterShowsDiscoveryProgressLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Loading = true
	lines := view.Render(120, 20)
	if !strings.Contains(strings.Join(lines, "\n"), "Loading additional tasks\u2026") {
		t.Fatalf("wide footer missing discovery progress: %#v", lines)
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, "Loading additional tasks\u2026") {
		t.Fatalf("progress row is not in the footer: %q", last)
	}

	narrow := New(sampleRows(), "", false)
	narrow.Loading = true
	narrowLines := narrow.Render(26, 20)
	if !strings.Contains(strings.Join(narrowLines, "\n"), "Loading tasks\u2026") {
		t.Fatalf("narrow footer missing discovery progress: %#v", narrowLines)
	}
	if strings.Contains(strings.Join(narrowLines, "\n"), "Loading additional tasks\u2026") {
		t.Fatalf("narrow footer kept the full label: %#v", narrowLines)
	}

	// A page request (Show more) is not initial discovery and keeps the hints.
	paging := New(sampleRows(), "", false)
	paging.Loading = true
	paging.SetShowMoreLoading(true)
	if lines := paging.Render(120, 20); strings.Contains(strings.Join(lines, "\n"), "Loading additional tasks\u2026") {
		t.Fatalf("show-more request rendered the discovery footer: %#v", lines)
	}

	// A notice owns the footer over progress, matching Rust's priority.
	notified := New(sampleRows(), "", false)
	notified.Loading = true
	notified.Notice = "Reconnecting"
	if lines := notified.Render(120, 20); strings.Contains(strings.Join(lines, "\n"), "Loading additional tasks\u2026") {
		t.Fatalf("notice did not take footer priority: %#v", lines)
	}
}
