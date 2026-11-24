package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/keymsg"
)

// Rust #63184: a model-requested preview is a snapshot. It paints the snapshot
// notice, keeps the content it read, and Enter in the focused pane subscribes it
// as a live, watched document.
func TestSidebarModelSnapshotOpensLiveOnEnterLikeRust(t *testing.T) {
	model, requests := newSidebarOpenModel(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "guide.md")
	if err := os.WriteFile(path, []byte("# One\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model.onSidebarDocument = func(request SidebarDocumentRequest) SidebarDocumentView {
		*requests = append(*requests, request)
		data, err := os.ReadFile(request.Path)
		if err != nil {
			return SidebarDocumentView{Path: request.Path, Err: err.Error()}
		}
		return SidebarDocumentView{Path: request.Path, Text: string(data)}
	}

	runTeaCmd(t, model, model.applySidebarOpenRequest(SidebarOpenRequestMsg{SourceThreadID: "thread-a", Path: path}))
	tab := model.sidebarDocumentActiveTab()
	if tab == nil || !tab.Snapshot || tab.AwaitingOpen {
		t.Fatalf("tab = %#v, want a loaded snapshot", tab)
	}
	painted := ansi.Strip(model.renderSidebarDocument(model.sidebarTabStripWidth(), 20))
	if !strings.Contains(painted, "Snapshot · Enter to open live") {
		t.Fatalf("snapshot render =\n%s", painted)
	}

	// A snapshot keeps what it read until the user opens it live.
	if err := os.WriteFile(path, []byte("# Two\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	bumpSidebarDocumentModTime(t, path)
	sweepSidebarDocumentWatch(t, model)
	if strings.Contains(tab.View.Text, "Two") {
		t.Fatalf("snapshot refreshed on its own: %q", tab.View.Text)
	}

	model.sidebarChooserFocused = true
	handled, cmd := model.updateSidebarDocumentKey(key(keymsg.KeyEnter))
	if !handled || cmd == nil {
		t.Fatalf("Enter on a snapshot handled=%v cmd=%t, want a live subscription", handled, cmd != nil)
	}
	runTeaCmd(t, model, cmd)
	if tab.Snapshot || tab.AwaitingOpen {
		t.Fatalf("tab after Enter = %#v, want a live document", tab)
	}
	if !strings.Contains(tab.View.Text, "Two") {
		t.Fatalf("opening live did not reload the file: %q", tab.View.Text)
	}
	if painted := ansi.Strip(model.renderSidebarDocument(model.sidebarTabStripWidth(), 20)); strings.Contains(painted, "Snapshot · Enter to open live") {
		t.Fatalf("live document kept the snapshot notice:\n%s", painted)
	}

	// The live document follows later edits.
	if err := os.WriteFile(path, []byte("# Three\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	bumpSidebarDocumentModTime(t, path)
	sweepSidebarDocumentWatch(t, model)
	if !strings.Contains(tab.View.Text, "Three") {
		t.Fatalf("live document did not refresh: %q", tab.View.Text)
	}
}

// Rust #63184: a backend without an in-process permissions reader asks before
// reading, and Enter in the focused pane performs the read the user confirmed.
func TestSidebarModelDocumentAwaitsOpenBeforeReadingLikeRust(t *testing.T) {
	model, requests := newSidebarOpenModel(t)
	model.onSidebarDocumentNeedsConfirmation = func(SidebarDocumentRequest) bool { return true }

	cmd := model.applySidebarOpenRequest(SidebarOpenRequestMsg{SourceThreadID: "thread-a", Path: "docs/readme.md", Line: 4})
	if cmd != nil {
		t.Fatal("an unconfirmed model request started a read")
	}
	if len(*requests) != 0 {
		t.Fatalf("document requests = %#v, want none before confirmation", *requests)
	}
	tab := model.sidebarDocumentActiveTab()
	if tab == nil || !tab.AwaitingOpen || !tab.Snapshot || tab.Loading {
		t.Fatalf("tab = %#v, want an awaiting-open snapshot", tab)
	}
	painted := ansi.Strip(model.renderSidebarDocument(80, 20))
	for _, want := range []string{"Open docs/readme.md at line 4?", "Focus this pane and press Enter to open."} {
		if !strings.Contains(painted, want) {
			t.Fatalf("awaiting-open render missing %q:\n%s", want, painted)
		}
	}

	model.sidebarChooserFocused = true
	handled, cmd := model.updateSidebarDocumentKey(key(keymsg.KeyEnter))
	if !handled || cmd == nil {
		t.Fatalf("Enter on awaiting-open handled=%v cmd=%t, want the confirmed read", handled, cmd != nil)
	}
	runTeaCmd(t, model, cmd)
	if len(*requests) != 1 || (*requests)[0].Path != "docs/readme.md" {
		t.Fatalf("document requests = %#v, want one confirmed read", *requests)
	}
	if tab.AwaitingOpen || tab.Snapshot || tab.Loading {
		t.Fatalf("tab after confirmation = %#v, want a live loaded document", tab)
	}
	if painted := ansi.Strip(model.renderSidebarDocument(model.sidebarTabStripWidth(), 20)); strings.Contains(painted, "press Enter to open") {
		t.Fatalf("confirmed document kept the prompt:\n%s", painted)
	}
}
