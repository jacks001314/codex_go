package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/tui/markdown"
)

// openChooserWithFiles opens the chooser on parent-a with a fixed inventory so
// the Files group can be measured without running the discovery callbacks.
func openChooserWithFiles(t *testing.T, model *Model, files []markdown.SidebarFileLink, cwd string) {
	t.Helper()
	model.sidebarChooserOpen = true
	model.sidebarChooserParent = "parent-a"
	model.sidebarChooserFocused = true
	model.sidebarFiles = files
	model.sidebarFilesCwd = cwd
	model.refreshTranscript()
}

// Rust sidebar_chooser.rs paint: the Files group sits under the chats with its
// own header and a wide row that keeps the file name and the directory suffix.
func TestSidebarChooserFilesGroupLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{
		{Path: "/workspace/src/components/App.tsx"},
	}, "/workspace")
	model.transcript.Height = 20

	rows := strings.Split(ansi.Strip(model.renderSidebarChooser(80, 20)), "\n")
	if rows[6] != " Files  1" {
		t.Fatalf("files header = %q, want row 6", rows[6])
	}
	if !strings.Contains(rows[8], "· App.tsx  src/components") {
		t.Fatalf("file row = %q", rows[8])
	}
	if _, ok := model.sidebarChooserFile(model.sidebarChooserFileStart()); !ok {
		t.Fatalf("file start %d does not map to the inventory", model.sidebarChooserFileStart())
	}
}

// Rust paint: a narrow body puts the directory on its own indented row, and a
// bare file name lives at the project root.
func TestSidebarChooserNarrowFileRowsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{
		{Path: "README.md"},
	}, "")
	model.transcript.Height = 20

	rows := strings.Split(ansi.Strip(model.renderSidebarChooser(40, 20)), "\n")
	if !strings.Contains(rows[9], "· README.md") {
		t.Fatalf("file row = %q", rows[9])
	}
	if rows[10] != "     Project root" {
		t.Fatalf("directory row = %q, want the project root", rows[10])
	}
}

// Rust paint: the header switches to "Markdown files" only when every entry is
// Markdown, and the preview constraint replaces the directory text.
func TestSidebarChooserMarkdownFilesHeaderLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{
		{Path: "/workspace/docs/readme.md"},
	}, "/workspace")
	model.transcript.Height = 20
	if rows := strings.Split(ansi.Strip(model.renderSidebarChooser(80, 20)), "\n"); rows[6] != " Markdown files  1" {
		t.Fatalf("markdown header = %q", rows[6])
	}

	model.sidebarFiles = []markdown.SidebarFileLink{
		{Path: "/workspace/docs/readme.md"},
		{Path: "/workspace/src/app.ts"},
	}
	model.sidebarFilePreviewUnavailable = "Preview unavailable on Windows"
	rows := strings.Split(ansi.Strip(model.renderSidebarChooser(80, 20)), "\n")
	if rows[6] != " Files  2" {
		t.Fatalf("mixed header = %q", rows[6])
	}
	if !strings.Contains(rows[8], "Preview unavailable on Windows") {
		t.Fatalf("file row = %q, want the preview constraint", rows[8])
	}
}

// Rust row_targets: file rows are not clickable when the preview is
// unavailable, and the accept hint disappears with them.
func TestSidebarChooserFileRowsInertWithoutPreviewLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{
		{Path: "/workspace/src/app.ts"},
	}, "/workspace")
	model.transcript.Height = 20
	fileRow := model.sidebarChoiceRowsFor(model.sidebarTabStripWidth(), 20).choices[model.sidebarChooserFileStart()].start
	if _, ok := model.sidebarChooserChoiceAtRow(fileRow); !ok {
		t.Fatal("file row is not clickable while previews are available")
	}

	model.sidebarFilePreviewUnavailable = "Preview unavailable on Windows"
	if _, ok := model.sidebarChooserChoiceAtRow(fileRow); ok {
		t.Fatal("file row stayed clickable without a preview")
	}
	model.sidebarChoice = model.sidebarChooserFileStart()
	footer := ansi.Strip(model.sidebarChooserFooter(sidebarChooserBodyWidth(80)))
	if strings.Contains(footer, "open") {
		t.Fatalf("footer = %q, want no open hint for an inert file", footer)
	}
	// Rust keeps the path hint: only Accept and the row target are gated on the
	// preview being available.
	if !strings.Contains(footer, " path") {
		t.Fatalf("footer = %q, want the path hint", footer)
	}
}

// Rust ChooserScroll: the wheel moves the viewport without touching the
// keyboard selection, and the next list move snaps back to it.
func TestSidebarChooserWheelScrollsFilesLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	files := make([]markdown.SidebarFileLink, 0, 40)
	for index := 0; index < 40; index++ {
		files = append(files, markdown.SidebarFileLink{Path: "/workspace/file-" + string(rune('a'+index%26)) + ".txt"})
	}
	openChooserWithFiles(t, model, files, "/workspace")
	model.transcript.Height = 14
	model.sidebarChoice = 0

	model.wheelScrollSidebarChooser(3)
	if model.sidebarChooserScroll.top <= 0 || !model.sidebarChooserScroll.wheelScrolled {
		t.Fatalf("wheel scroll = %#v, want a scrolled viewport", model.sidebarChooserScroll)
	}
	if model.sidebarChoice != 0 {
		t.Fatalf("wheel moved the selection to %d", model.sidebarChoice)
	}
	model.navigateSidebarChooser("move_down")
	if model.sidebarChoice != 1 || model.sidebarChooserScroll.wheelScrolled {
		t.Fatalf("keyboard move choice=%d scroll=%#v, want a snapped selection", model.sidebarChoice, model.sidebarChooserScroll)
	}
	if !model.sidebarChooserSelectionVisible() {
		t.Fatal("keyboard move left the selection outside the painted window")
	}
}

// Rust paint's full_path view: `p` opens it, page keys scroll the wrapped path,
// and Cancel closes the view instead of the chooser.
func TestSidebarChooserFilePathViewLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	long := "/workspace/" + strings.Repeat("nested-directory/", 30) + "app.tsx"
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{{Path: long}}, "/workspace")
	model.transcript.Height = 8
	model.sidebarChoice = model.sidebarChooserFileStart()

	model.Update(keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune{'p'}})
	if !model.sidebarShowingFilePath {
		t.Fatal("`p` did not open the file-path view")
	}
	panel := ansi.Strip(model.renderSidebarChooser(60, 8))
	if !strings.Contains(panel, "File path") || !strings.Contains(panel, "nested-directory") {
		t.Fatalf("path view =\n%s", panel)
	}
	// The toggle refreshes the transcript viewport, so pin the pane height the
	// scroll helper measures before paging.
	model.transcript.Height = 8
	model.Update(key(keymsgPageDown()))
	if model.sidebarFilePathScroll == 0 {
		t.Fatal("page_down did not scroll the wrapped path")
	}
	model.Update(key(keymsg.KeyEsc))
	if model.sidebarShowingFilePath {
		t.Fatal("esc did not close the file-path view")
	}
	if !model.sidebarChooserVisible() {
		t.Fatal("esc closed the chooser instead of only the path view")
	}
}

// Rust SidebarPane::restore_file_selection: when the inventory grows above the
// selected file, the selection follows the path.
func TestSidebarChooserKeepsFileSelectionWhenInventoryChangesLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	openChooserWithFiles(t, model, []markdown.SidebarFileLink{
		{Path: "/workspace/b.ts"},
	}, "/workspace")
	model.sidebarChoice = model.sidebarChooserFileStart()
	if got := model.sidebarChooserSelectedFilePath(); got != "/workspace/b.ts" {
		t.Fatalf("selected file = %q", got)
	}

	model.applySidebarFilesLoaded(SidebarFilesLoadedMsg{
		ParentThreadID: "parent-a",
		Snapshot: SidebarFilesSnapshot{
			Files: []markdown.SidebarFileLink{
				{Path: "/workspace/a.ts"},
				{Path: "/workspace/b.ts"},
			},
			CWD: "/workspace",
		},
	})
	if got := model.sidebarChooserSelectedFilePath(); got != "/workspace/b.ts" {
		t.Fatalf("selection after the inventory grew = %q, want b.ts", got)
	}
}

// Rust refresh_sidebar_files transcript pass: markdown links from visible user
// and agent messages join the workspace and list newest first.
func TestSidebarChooserListsTranscriptLinksLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.State.CWD = "/workspace"
	model.State.AddMessage(codextui.RoleUser, "see [first](src/first.ts) and [root](README.md)")
	model.State.AddMessage(codextui.RoleAssistant, "updated [second](/workspace/src/second.ts)")

	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	got := make([]string, 0, len(model.sidebarFiles))
	for _, file := range model.sidebarFiles {
		got = append(got, file.Path)
	}
	want := []string{"/workspace/src/second.ts", "/workspace/README.md", "/workspace/src/first.ts"}
	if len(got) != len(want) {
		t.Fatalf("transcript files = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("transcript files = %v, want %v", got, want)
		}
	}
}

// Rust promote_files: the host-provided inventory (patches, generated images)
// follows the transcript links without duplicating a mentioned file.
func TestSidebarChooserMergesHostFilesLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.State.CWD = "/workspace"
	model.State.AddMessage(codextui.RoleAssistant, "see [app](src/app.ts)")
	model.onSidebarFiles = func(SidebarFilesRequest) SidebarFilesSnapshot {
		return SidebarFilesSnapshot{
			Files: []markdown.SidebarFileLink{
				{Path: "/workspace/src/app.ts"},
				{Path: "/workspace/src/generated.ts"},
			},
			CWD: "/workspace",
		}
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	if len(model.sidebarFiles) != 2 {
		t.Fatalf("merged files = %+v, want two unique entries", model.sidebarFiles)
	}
	if model.sidebarFiles[0].Path != "/workspace/src/app.ts" || model.sidebarFiles[1].Path != "/workspace/src/generated.ts" {
		t.Fatalf("merged files = %+v, want the transcript link first", model.sidebarFiles)
	}
}

// Rust notice_sidebar_file_item: a completed patch is remembered while the
// chooser is closed and heads the Files group when it opens.
func TestSidebarChooserPromotesPatchFilesLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.State.CWD = "/workspace"
	model.State.AddMessage(codextui.RoleAssistant, "see [app](src/app.ts)")
	model.Update(SidebarFilePromotedMsg{
		ParentThreadID: "parent-a",
		Entries:        []markdown.SidebarFileLink{{Path: "/workspace/src/fixed.ts"}},
	})
	if len(model.sidebarFiles) != 0 {
		t.Fatalf("closed chooser painted %+v", model.sidebarFiles)
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	if len(model.sidebarFiles) != 2 {
		t.Fatalf("files = %+v, want the patch and the transcript link", model.sidebarFiles)
	}
	if model.sidebarFiles[0].Path != "/workspace/src/fixed.ts" || model.sidebarFiles[1].Path != "/workspace/src/app.ts" {
		t.Fatalf("files = %+v, want the patch first", model.sidebarFiles)
	}
}

// keymsgPageDown keeps the path-view test readable while spelling the key the
// shared list bindings resolve (ctrl+f).
func keymsgPageDown() keymsg.KeyType { return keymsg.KeyCtrlF }

// Rust refresh_sidebar_files + start_sidebar_files_scan: opening the chooser
// restarts the scan, and each snapshot that still has history schedules the
// next page instead of reopening the chooser.
func TestSidebarChooserChainsSidebarFilePagesLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	var requests []SidebarFilesRequest
	model.onSidebarFiles = func(request SidebarFilesRequest) SidebarFilesSnapshot {
		requests = append(requests, request)
		if len(requests) == 1 {
			return SidebarFilesSnapshot{
				Files:   []markdown.SidebarFileLink{{Path: "/workspace/newest.ts"}},
				Loading: true,
				CWD:     "/workspace",
			}
		}
		return SidebarFilesSnapshot{
			Files: []markdown.SidebarFileLink{
				{Path: "/workspace/newest.ts"},
				{Path: "/workspace/older.ts"},
			},
			CWD: "/workspace",
		}
	}
	if cmd := model.openSidebarChooser(); cmd != nil {
		runTeaCmdChain(t, model, cmd, 0)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %+v, want the open plus one page", requests)
	}
	if !requests[0].Resume {
		t.Fatal("the first request must resume the scan")
	}
	if requests[1].Resume {
		t.Fatal("a page request must continue the scan")
	}
	if requests[1].ParentThreadID != "parent-a" {
		t.Fatalf("page parent = %q", requests[1].ParentThreadID)
	}
	if len(model.sidebarFiles) != 2 || model.sidebarFiles[1].Path != "/workspace/older.ts" {
		t.Fatalf("files = %+v, want both pages", model.sidebarFiles)
	}
	if model.sidebarFilesLoading {
		t.Fatal("an exhausted scan must clear the loading notice")
	}
}
