package tea

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/keymsg"
)

// Rust #63388 path_actions: a copy that cannot be confirmed reveals the full
// path, the footer reports the failure, and Esc returns to the preview without
// closing the tab.
func TestSidebarDocumentPathCopyRevealsMissingFileLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.transcript.Height = 12
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path: "/workspace/docs/missing.md",
		Err:  "no such file or directory (os error 2)",
	})
	var copied []string
	model.clipboardWrite = func(text string) error {
		copied = append(copied, text)
		return errors.New("no clipboard")
	}
	model.clipboardForward = func(string, bool) error { return errors.New("no terminal") }

	handled, cmd := model.updateSidebarDocumentKey(runes("c"))
	if !handled || cmd == nil {
		t.Fatalf("`c` handled=%t cmd=%v", handled, cmd)
	}
	runTeaCmd(t, model, cmd)

	if len(copied) != 1 || copied[0] != "/workspace/docs/missing.md" {
		t.Fatalf("copied = %#v", copied)
	}
	if tab.PathScroll == nil {
		t.Fatal("a failed copy did not reveal the path")
	}
	width := 80
	rows := strings.Split(ansi.Strip(model.renderSidebarDocument(width, 12)), "\n")
	painted := strings.Join(rows, "\n")
	if !strings.Contains(rows[2], "File path") {
		t.Fatalf("path caption row = %q", rows[2])
	}
	if !strings.Contains(painted, "/workspace/docs/missing.md") {
		t.Fatalf("path view =\n%s", painted)
	}
	footer := rows[11]
	for _, want := range []string{"Could not copy path", "p/esc hide path", "c copy path"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("path footer = %q, want %q", footer, want)
		}
	}

	model.updateSidebarDocumentKey(keymsg.KeyMsg{Type: keymsg.KeyEsc})
	if tab.PathScroll != nil {
		t.Fatal("esc did not dismiss the path view")
	}
	if !model.sidebarDocumentVisible() {
		t.Fatal("esc closed the tab instead of only the path view")
	}
	if preview := ansi.Strip(model.renderSidebarDocument(width, 12)); !strings.Contains(preview, "File not found") {
		t.Fatalf("preview after esc =\n%s", preview)
	}
}

// Rust #63388: `p` opens the scrollable path view, vertical list keys scroll
// it, and a second `p` closes it again.
func TestSidebarDocumentPathViewScrollsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.transcript.Height = 12
	long := "/workspace/" + strings.Repeat("nested-directory/", 40) + "app.tsx"
	tab := openDocumentTabAt(t, model, SidebarDocumentView{Path: long, Text: "# Doc\n"})

	if handled, _ := model.updateSidebarDocumentKey(runes("p")); !handled || tab.PathScroll == nil {
		t.Fatalf("`p` handled=%t scroll=%v", handled, tab.PathScroll)
	}
	if painted := ansi.Strip(model.renderSidebarDocument(80, 12)); !strings.Contains(painted, "File path") {
		t.Fatalf("path view =\n%s", painted)
	}
	model.transcript.Height = 12
	model.updateSidebarDocumentKey(keymsg.KeyMsg{Type: keymsg.KeyCtrlF})
	if tab.PathScroll == nil || *tab.PathScroll == 0 {
		t.Fatalf("page down scroll = %v", tab.PathScroll)
	}
	if handled, _ := model.updateSidebarDocumentKey(runes("p")); !handled || tab.PathScroll != nil {
		t.Fatalf("second `p` handled=%t scroll=%v", handled, tab.PathScroll)
	}
	if !model.sidebarDocumentVisible() {
		t.Fatal("toggling the path view closed the tab")
	}
}

// Rust #63388 footer_hint::wrapped: the reader hints keep their first entry,
// the path shortcuts follow it, and the overflow continues on a second row.
func TestSidebarDocumentFooterWrapsPathHintsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.transcript.Height = 12
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path: "/workspace/docs/guide.md",
		Text: "# Guide\n\nBody\n",
	})
	if tab.PathScroll != nil {
		t.Fatal("a fresh preview starts with the path view closed")
	}

	rows := strings.Split(ansi.Strip(model.renderSidebarDocument(60, 12)), "\n")
	first, second := rows[10], rows[11]
	if !strings.HasPrefix(first, " esc close · c copy path · p show path") {
		t.Fatalf("first footer row = %q", first)
	}
	if !strings.Contains(second, "select") {
		t.Fatalf("second footer row = %q", second)
	}
	if strings.Contains(second, "c copy path") {
		t.Fatalf("path hints repeated on the second row: %q", second)
	}
	if !strings.Contains(ansi.Strip(model.renderSidebarDocument(29, 12)), "…") {
		t.Fatal("a narrow pane did not mark the truncated footer")
	}
}
