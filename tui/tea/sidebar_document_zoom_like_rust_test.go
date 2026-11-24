package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/markdown"
)

// Rust #61889 sidebar::content_layout: toggle_pane_zoom on a preview keeps the
// sidebar and gives the document the whole workspace, so the conversation is
// not painted beside it. Closing the preview gives the split back.
func TestSidebarDocumentZoomTakesTheWorkspaceLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{{Path: "docs/readme.md"}}, "")
	model.onSidebarDocument = func(SidebarDocumentRequest) SidebarDocumentView {
		return SidebarDocumentView{Path: "/workspace/docs/readme.md", Text: "Zoomed preview body"}
	}
	model.sidebarChoice = model.sidebarChooserFileStart()
	runTeaCmd(t, model, model.acceptSidebarChooser())
	if !model.sidebarDocumentVisible() {
		t.Fatal("chooser did not open the preview")
	}

	model.togglePaneZoom()
	if !model.sideZoomed {
		t.Fatal("a document preview could not zoom")
	}
	left, right, focused, ok := model.sidePaneLayout(model.width)
	if !ok || left != 0 || right != model.width || !focused {
		t.Fatalf("zoomed layout = left %d right %d focused %t ok %t, want the preview across %d columns", left, right, focused, ok, model.width)
	}
	rendered, ok := model.renderSideWorkspace(model.width, 30)
	if !ok {
		t.Fatal("the zoomed preview fell back to the single-conversation path")
	}
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "Zoomed preview body") {
		t.Fatalf("zoomed workspace lost the preview:\n%s", plain)
	}
	if strings.Contains(plain, "hello from the main chat") {
		t.Fatalf("zoomed workspace still painted the conversation:\n%s", plain)
	}
	if strings.Contains(plain, sideWorkspaceDivider) {
		t.Fatalf("zoomed workspace painted a divider:\n%s", plain)
	}

	model.togglePaneZoom()
	if model.sideZoomed {
		t.Fatal("the second toggle kept the document zoom")
	}
	if left, _, _, ok := model.sidePaneLayout(model.width); !ok || left <= 0 {
		t.Fatalf("unzoomed layout = left %d ok %t, want the split back", left, ok)
	}

	// A zoom belongs to the preview that owned the workspace; closing that
	// preview must not leave the conversation zoomed.
	model.togglePaneZoom()
	tab := model.sidebarDocumentActiveTab()
	if tab == nil {
		t.Fatal("no active preview tab to close")
	}
	runTeaCmd(t, model, model.closeSidebarDocumentTab(tab.ID))
	if model.sideZoomed {
		t.Fatal("closing the preview kept the workspace zoom")
	}
}
