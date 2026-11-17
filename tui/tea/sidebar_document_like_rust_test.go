package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/keymsg"
	"codex_go/tui/markdown"
	mousemsg "codex_go/tui/mousemsg"
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

// openDocumentTabAt installs one loaded preview tab and makes it the visible
// content so link navigation can be exercised without a chooser round trip.
func openDocumentTabAt(t *testing.T, model *Model, view SidebarDocumentView) *sidebarDocumentTab {
	t.Helper()
	tab := &sidebarDocumentTab{ID: "doc-1", Parent: "parent-a", Source: "parent-a", Path: view.Path, View: view, Link: -1, HoverLink: -1}
	model.sidebarChooserOpen = true
	model.sidebarChooserParent = "parent-a"
	model.sidebarChooserFocused = true
	model.sidebarDocuments["parent-a"] = []*sidebarDocumentTab{tab}
	model.sidebarDocumentActive = tab.ID
	model.refreshTranscript()
	return tab
}

// Rust sidebar_document.rs `cycle_document_link` + `OpenDocument`: Tab selects
// the links in order and Enter opens the selected one against the preview's
// directory.
func TestSidebarDocumentLinkCycleAndFollowLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	var requests []SidebarDocumentRequest
	model.onSidebarDocument = func(request SidebarDocumentRequest) SidebarDocumentView {
		requests = append(requests, request)
		return SidebarDocumentView{Path: request.Path, Text: "package main\n"}
	}
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path: "/workspace/docs/readme.md",
		Text: "# Doc\n\n[one](a.md) and [two](b.go#L2C1)\n",
	})

	if handled, _ := model.updateSidebarDocumentKey(keymsg.KeyMsg{Type: keymsg.KeyTab}); !handled || tab.Link != 0 {
		t.Fatalf("first tab = handled %t link %d", handled, tab.Link)
	}
	model.updateSidebarDocumentKey(keymsg.KeyMsg{Type: keymsg.KeyTab})
	if tab.Link != 1 {
		t.Fatalf("second tab link = %d", tab.Link)
	}
	model.updateSidebarDocumentKey(keymsg.KeyMsg{Type: keymsg.KeyShiftTab})
	if tab.Link != 0 {
		t.Fatalf("backtab link = %d", tab.Link)
	}
	model.updateSidebarDocumentKey(keymsg.KeyMsg{Type: keymsg.KeyTab})

	cmd := model.followSidebarDocumentLink(tab)
	if cmd == nil {
		t.Fatal("enter did not follow the selected link")
	}
	runTeaCmd(t, model, cmd)
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	if requests[0].Path != "/workspace/docs/b.go" || requests[0].Location == nil || requests[0].Location.Line != 2 {
		t.Fatalf("request = %#v", requests[0])
	}
}

// Rust Document::go_to / Esc: the first Esc clears the coordinate highlight
// and the next one closes the tab.
func TestSidebarDocumentEscClearsHighlightLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path:       "/workspace/main.go",
		Text:       "package main\n\nfunc main() {}\n",
		Code:       true,
		Language:   "go",
		LineStarts: []int{0, 13, 14},
		Target:     &SidebarDocumentTarget{Line: 3, Column: 1, Offset: 14},
	})

	model.updateSidebarDocumentKey(keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b})
	if !tab.TargetCleared || !model.sidebarDocumentVisible() {
		t.Fatalf("first esc cleared=%t visible=%t", tab.TargetCleared, model.sidebarDocumentVisible())
	}
	model.updateSidebarDocumentKey(keymsg.KeyMsg{Type: keymsg.KeyEsc, Code: 0x1b})
	if model.sidebarDocumentVisible() {
		t.Fatal("second esc did not close the tab")
	}
}

// Rust LegacyAppPathString::resolve_against: relative link targets join the
// preview's directory and absolute ones stay as written.
func TestSidebarDocumentLinkPathResolutionLikeRust(t *testing.T) {
	cases := []struct{ document, link, want string }{
		{"/workspace/docs/readme.md", "b.go", "/workspace/docs/b.go"},
		{"/workspace/docs/readme.md", "../src/app.go", "/workspace/src/app.go"},
		{"/workspace/docs/readme.md", "/etc/hosts", "/etc/hosts"},
		{`C:\work\docs\readme.md`, "b.go", `C:\work\docs\b.go`},
	}
	for _, testCase := range cases {
		if got := resolveSidebarDocumentLinkPath(testCase.document, testCase.link); got != testCase.want {
			t.Fatalf("resolve(%q, %q) = %q, want %q", testCase.document, testCase.link, got, testCase.want)
		}
	}
}

// Rust #61890 pointer path: hovering a painted destination emphasizes it, and
// a left press opens that link with the same geometry as the Tab selection.
func TestSidebarDocumentHoverAndClickOpenLinkLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.transcript.Height = 20
	var requests []SidebarDocumentRequest
	model.onSidebarDocument = func(request SidebarDocumentRequest) SidebarDocumentView {
		requests = append(requests, request)
		return SidebarDocumentView{Path: request.Path, Text: "package main\n"}
	}
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path: "/workspace/docs/readme.md",
		Text: "# Doc\n\n[one](a.md) and [two](b.go#L2C1)\n",
	})
	linkRows := model.sidebarDocumentLinkRows(tab)
	if len(linkRows) != 2 || linkRows[1].Row < 0 {
		t.Fatalf("link rows = %#v", linkRows)
	}
	width := model.sidebarTabStripWidth()
	left, _, _, ok := model.sidePaneLayout(model.width)
	if !ok {
		t.Fatal("side pane layout unavailable")
	}
	x := left + ansi.StringWidth(sideWorkspaceDivider) + 1 + linkRows[1].Column
	y := linkRows[1].Row - tab.Scroll + 1

	if handled, _ := model.handleSidePaneMouse(mousemsg.MouseMsg{X: x, Y: y, Action: mousemsg.MouseActionMotion}); !handled {
		t.Fatal("motion over the preview was not handled")
	}
	if tab.HoverLink != 1 {
		t.Fatalf("hover link = %d, want 1", tab.HoverLink)
	}
	if painted := model.renderSidebarDocument(width, 20); !strings.Contains(painted, "\x1b[1m") {
		t.Fatal("hovered destination was not emphasized")
	}

	_, cmd := model.handleSidePaneMouse(mousemsg.MouseMsg{
		X: x, Y: y, Action: mousemsg.MouseActionPress, Button: mousemsg.MouseButtonLeft,
	})
	if cmd == nil {
		t.Fatal("click on a painted destination did not open it")
	}
	runTeaCmd(t, model, cmd)
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	if requests[0].Path != "/workspace/docs/b.go" || requests[0].Location == nil || requests[0].Location.Line != 2 {
		t.Fatalf("request = %#v", requests[0])
	}
	if tab.HoverLink != -1 {
		t.Fatalf("hover after opening = %d", tab.HoverLink)
	}
}
