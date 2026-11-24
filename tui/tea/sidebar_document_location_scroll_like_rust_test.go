package tea

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Rust #61890 jump_to_offset: opening a source link at a line and column
// scrolls the preview to the target instead of leaving the viewport at the top.
func TestSidebarDocumentLocationScrollsToTargetLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	tab := &sidebarDocumentTab{ID: "doc-1", Parent: "parent-a", Source: "parent-a", Path: "/workspace/main.go", Link: -1, HoverLink: -1}
	model.sidebarChooserOpen = true
	model.sidebarChooserParent = "parent-a"
	model.sidebarChooserFocused = true
	model.sidebarDocuments["parent-a"] = []*sidebarDocumentTab{tab}
	model.sidebarDocumentActive = tab.ID
	model.transcript.Height = 20

	lines := make([]string, 200)
	for index := range lines {
		lines[index] = fmt.Sprintf("body line %03d", index+1)
	}
	runTeaCmd(t, model, model.applySidebarDocumentLoaded(SidebarDocumentLoadedMsg{
		ParentThreadID: "parent-a",
		ID:             tab.ID,
		View: SidebarDocumentView{
			Path:   "/workspace/main.go",
			Text:   strings.Join(lines, "\n"),
			Code:   true,
			Target: &SidebarDocumentTarget{Line: 150, Column: 3},
		},
	}))

	if tab.Scroll <= 0 {
		t.Fatal("a located preview left the viewport at the top")
	}
	rendered := ansi.Strip(model.renderSidebarDocument(60, 20))
	if !strings.Contains(rendered, "body line 150") {
		t.Fatalf("located preview did not show the target line:\n%s", rendered)
	}
	if strings.Contains(rendered, "body line 001") {
		t.Fatalf("located preview still painted the first line:\n%s", rendered)
	}
}
