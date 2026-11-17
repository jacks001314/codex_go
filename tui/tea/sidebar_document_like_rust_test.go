package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/keymsg"
	"codex_go/tui/markdown"
)

// Rust sidebar_document.rs `open_resolved_document`: accepting a chooser file
// row opens a preview tab, paints the loading state, and swaps in the read
// result when the load command reports back.
func TestSidebarChooserOpensDocumentTabLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{{Path: "docs/readme.md"}}, "")
	var requests []SidebarDocumentRequest
	model.onSidebarDocument = func(request SidebarDocumentRequest) SidebarDocumentView {
		requests = append(requests, request)
		return SidebarDocumentView{Path: "/workspace/docs/readme.md", Text: "# Title\n\nBody"}
	}
	model.sidebarChoice = model.sidebarChooserFileStart()

	cmd := model.acceptSidebarChooser()
	if cmd == nil {
		t.Fatal("accepting a file row returned no load command")
	}
	if !model.sidebarDocumentVisible() {
		t.Fatal("file row did not open a document tab")
	}
	if model.sidebarChooserVisible() {
		t.Fatal("the preview should replace the chooser list")
	}
	if rows := strings.Split(ansi.Strip(model.renderSidebarDocument(60, 8)), "\n"); !strings.Contains(rows[1], "Opening…") {
		t.Fatalf("loading row = %q", rows[1])
	}

	runTeaCmd(t, model, cmd)
	if len(requests) != 1 {
		t.Fatalf("document requests = %d, want one", len(requests))
	}
	if requests[0].ParentThreadID != "parent-a" || requests[0].Path != "docs/readme.md" || requests[0].Link != -1 {
		t.Fatalf("request = %#v", requests[0])
	}
	rows := strings.Split(ansi.Strip(model.renderSidebarDocument(60, 8)), "\n")
	if !strings.Contains(strings.Join(rows, "\n"), "Title") {
		t.Fatalf("rendered preview = %q", rows)
	}
}

// Rust sidebar_document.rs `loaded`: source previews keep a line-number gutter
// only when the pane is wider than 40 columns plus the digit count.
func TestSidebarDocumentCodeGutterLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	text := "package main\n\nfunc main() {\n}\n"
	view := SidebarDocumentView{
		Path:       "/workspace/main.go",
		Text:       text,
		Code:       true,
		Language:   "go",
		LineStarts: []int{0, 13, 14, 28},
	}
	tab := &sidebarDocumentTab{ID: "doc", Parent: "parent-a", Path: view.Path, View: view}
	model.sidebarDocuments["parent-a"] = []*sidebarDocumentTab{tab}
	model.sidebarDocumentActive = tab.ID

	wide := strings.Split(ansi.Strip(model.renderSidebarDocument(80, 8)), "\n")
	if !strings.HasPrefix(wide[1], " 1 ") {
		t.Fatalf("wide gutter row = %q", wide[1])
	}
	if !strings.HasPrefix(wide[3], " 3 ") {
		t.Fatalf("wide third row = %q", wide[3])
	}

	model.sidebarDocuments["parent-a"][0].rendered = false
	narrow := strings.Split(ansi.Strip(model.renderSidebarDocument(40, 8)), "\n")
	if strings.HasPrefix(narrow[1], " 1 ") {
		t.Fatalf("narrow pane kept the gutter: %q", narrow[1])
	}
}

// Rust sidebar.rs `document_title`: the tab label starts at the file name and
// widens until two open documents no longer share it.
func TestSidebarDocumentTabTitlesLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	first := &sidebarDocumentTab{ID: "doc-1", Parent: "parent-a", Path: "/workspace/src/app.go"}
	second := &sidebarDocumentTab{ID: "doc-2", Parent: "parent-a", Path: "/workspace/lib/app.go"}
	model.sidebarDocuments["parent-a"] = []*sidebarDocumentTab{first, second}

	if title := model.sidebarDocumentTitle(first, 30); title != "src/app.go" {
		t.Fatalf("first title = %q", title)
	}
	if title := model.sidebarDocumentTitle(second, 30); title != "lib/app.go" {
		t.Fatalf("second title = %q", title)
	}
	model.sidebarDocuments["parent-a"] = []*sidebarDocumentTab{first}
	if title := model.sidebarDocumentTitle(first, 30); title != "app.go" {
		t.Fatalf("single title = %q", title)
	}
}

// Rust side_focus.rs `close_sidebar_tab`: closing the visible preview returns
// the pane to the chooser that opened it.
func TestSidebarDocumentEscReturnsToChooserLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{{Path: "docs/readme.md"}}, "")
	model.onSidebarDocument = func(SidebarDocumentRequest) SidebarDocumentView {
		return SidebarDocumentView{Path: "/workspace/docs/readme.md", Text: "Body"}
	}
	model.sidebarChoice = model.sidebarChooserFileStart()
	runTeaCmd(t, model, model.acceptSidebarChooser())
	if !model.sidebarDocumentVisible() {
		t.Fatal("document did not open")
	}

	esc := keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b}
	if handled, _ := model.updateSidebarDocumentKey(esc); !handled {
		t.Fatal("esc was not consumed by the reader")
	}
	if model.sidebarDocumentVisible() {
		t.Fatal("esc did not close the preview tab")
	}
	if !model.sidebarChooserVisible() || !model.sidebarChooserFocused {
		t.Fatal("closing the preview did not restore the chooser")
	}
	if !model.sidebarTabStripVisible() {
		t.Fatal("tab strip disappeared after closing the preview")
	}
}

// Rust side_focus.rs: a preview tab paints after the chat tabs and carries the
// document title.
func TestSidebarDocumentTabPaintsInStripLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{{Path: "/workspace/docs/readme.md"}}, "/workspace")
	model.onSidebarDocument = func(SidebarDocumentRequest) SidebarDocumentView {
		return SidebarDocumentView{Path: "/workspace/docs/readme.md", Text: "Body"}
	}
	model.sidebarChoice = model.sidebarChooserFileStart()
	runTeaCmd(t, model, model.acceptSidebarChooser())

	strip := ansi.Strip(model.renderSidebarTabStrip(80))
	if !strings.Contains(strip, "readme.md") {
		t.Fatalf("strip = %q, want the document title", strip)
	}
	window := model.sidebarTabStripWindowWidth(80)
	if window.total() != 1 || !window.isDocument(0) {
		t.Fatalf("window total=%d docs=%d", window.total(), len(window.docs))
	}
}

// Rust side_focus.rs: a row in a workspace that cannot preview ignores accept.
func TestSidebarChooserPreviewUnavailableIgnoresAcceptLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{{Path: "/workspace/docs/readme.md"}}, "/workspace")
	model.sidebarFilePreviewUnavailable = "Preview unavailable on Windows"
	opened := 0
	model.onSidebarDocument = func(SidebarDocumentRequest) SidebarDocumentView {
		opened++
		return SidebarDocumentView{}
	}
	model.sidebarChoice = model.sidebarChooserFileStart()
	runTeaCmd(t, model, model.acceptSidebarChooser())
	if opened != 0 {
		t.Fatalf("preview unavailable still opened %d documents", opened)
	}
	if model.sidebarDocumentVisible() {
		t.Fatal("preview unavailable opened a tab")
	}
}

// Rust sidebar::SidebarContent::Document: the preview paints in the sidebar
// column while the parent conversation keeps the left one.
func TestSidebarDocumentPaintsInWorkspaceLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{{Path: "docs/readme.md"}}, "")
	model.onSidebarDocument = func(SidebarDocumentRequest) SidebarDocumentView {
		return SidebarDocumentView{Path: "/workspace/docs/readme.md", Text: "Unique preview body"}
	}
	model.sidebarChoice = model.sidebarChooserFileStart()
	runTeaCmd(t, model, model.acceptSidebarChooser())

	rendered, ok := model.renderSideWorkspace(120, 30)
	if !ok {
		t.Fatal("document did not reuse the split workspace")
	}
	if !strings.Contains(ansi.Strip(rendered), "Unique preview body") {
		t.Fatalf("workspace = %q", ansi.Strip(rendered))
	}
	if left, _, focused, ok := model.sidePaneLayout(120); !ok || !focused || left <= 0 {
		t.Fatalf("layout = %d focused=%t ok=%t", left, focused, ok)
	}
}

// Rust step_sidebar_tab: sidebar_up/down walk the strip while a preview owns
// the pane, so the next chat tab can be reached without closing the reader.
func TestSidebarDocumentTabStepLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{{Path: "docs/readme.md"}}, "")
	model.onSidebarDocument = func(SidebarDocumentRequest) SidebarDocumentView {
		return SidebarDocumentView{Path: "/workspace/docs/readme.md", Text: "Body"}
	}
	model.sidebarChoice = model.sidebarChooserFileStart()
	runTeaCmd(t, model, model.acceptSidebarChooser())
	model.sidebarTabs["parent-a"] = []*activeSideConversation{{ParentThreadID: "parent-a", SideThreadID: "side-a"}}

	model.moveSidebarChooserChoice(1)
	if model.sidebarDocumentVisible() {
		t.Fatal("stepping did not leave the preview")
	}
	if model.activeSide == nil || !model.activeSide.ShowingSide {
		t.Fatal("stepping did not select the chat tab")
	}
}
