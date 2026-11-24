package tea

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Rust #61889 TranscriptView::paint_document_scroll_indicators: a preview
// scrolled away from an end shows a dim arrow in the body's last column.
func TestSidebarDocumentScrollIndicatorsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	tab := &sidebarDocumentTab{ID: "doc-1", Parent: "parent-a", Source: "parent-a", Path: "/workspace/main.go", Link: -1, HoverLink: -1}
	model.sidebarChooserOpen = true
	model.sidebarChooserParent = "parent-a"
	model.sidebarChooserFocused = true
	model.sidebarDocuments["parent-a"] = []*sidebarDocumentTab{tab}
	model.sidebarDocumentActive = tab.ID
	model.transcript.Height = 20

	lines := make([]string, 100)
	for index := range lines {
		lines[index] = fmt.Sprintf("body line %03d", index+1)
	}
	runTeaCmd(t, model, model.applySidebarDocumentLoaded(SidebarDocumentLoadedMsg{
		ParentThreadID: "parent-a",
		ID:             tab.ID,
		View:           SidebarDocumentView{Path: "/workspace/main.go", Text: strings.Join(lines, "\n"), Code: true},
	}))

	// The footer advertises the read keys, so only the body rows carry the
	// painted arrows.
	bodyEdges := func() (string, string) {
		t.Helper()
		rendered := strings.Split(ansi.Strip(model.renderSidebarDocument(60, 20)), "\n")
		first, last := "", ""
		for _, row := range rendered {
			if !strings.Contains(row, "body line") {
				continue
			}
			if first == "" {
				first = row
			}
			last = row
		}
		if first == "" {
			t.Fatalf("no body rows painted:\n%s", strings.Join(rendered, "\n"))
		}
		return strings.TrimRight(first, " "), strings.TrimRight(last, " ")
	}

	first, last := bodyEdges()
	if strings.HasSuffix(first, "\u2191") {
		t.Fatalf("an unscrolled preview painted the up arrow: %q", first)
	}
	if !strings.HasSuffix(last, "\u2193") {
		t.Fatalf("an unscrolled preview did not paint the down arrow: %q", last)
	}

	tab.Scroll = 50
	first, last = bodyEdges()
	if !strings.HasSuffix(first, "\u2191") || !strings.HasSuffix(last, "\u2193") {
		t.Fatalf("a scrolled preview edges = %q / %q, want both arrows", first, last)
	}

	tab.Scroll = 1 << 20
	first, last = bodyEdges()
	if !strings.HasSuffix(first, "\u2191") {
		t.Fatalf("the end of the preview did not paint the up arrow: %q", first)
	}
	if strings.HasSuffix(last, "\u2193") {
		t.Fatalf("the end of the preview still painted the down arrow: %q", last)
	}
}
