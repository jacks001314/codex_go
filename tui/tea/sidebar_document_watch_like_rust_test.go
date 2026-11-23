package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Rust #62583 watch.rs: an edited file refreshes the open preview at the same
// reading position, a removed file reports the failure, and the preview
// recovers when the file returns.
func TestSidebarDocumentPreviewRefreshesChangedFileLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "guide.md")
	if err := os.WriteFile(path, []byte("# One\n\nalpha\n\nbeta\n\ngamma\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	model.onSidebarDocument = func(request SidebarDocumentRequest) SidebarDocumentView {
		data, err := os.ReadFile(request.Path)
		if err != nil {
			return SidebarDocumentView{Path: request.Path, Err: err.Error()}
		}
		return SidebarDocumentView{Path: request.Path, Text: string(data)}
	}

	runTeaCmd(t, model, model.openSidebarDocumentAt(path, -1, nil))
	tab := model.sidebarDocumentActiveTab()
	if tab == nil {
		t.Fatal("the preview did not open")
	}
	if !strings.Contains(tab.View.Text, "alpha") {
		t.Fatalf("initial preview = %q", tab.View.Text)
	}
	// An unchanged file does not reload.
	if cmds := model.sidebarDocumentReloadChangedCmds(); len(cmds) != 0 {
		t.Fatalf("unchanged file scheduled %d reloads", len(cmds))
	}
	tab.Scroll = 2

	if err := os.WriteFile(path, []byte("# Two\n\nalpha\n\nbeta\n\ndelta\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	bumpSidebarDocumentModTime(t, path)
	sweepSidebarDocumentWatch(t, model)
	if !strings.Contains(tab.View.Text, "delta") || strings.Contains(tab.View.Text, "gamma") {
		t.Fatalf("refreshed preview = %q", tab.View.Text)
	}
	if tab.Scroll != 2 {
		t.Fatalf("refresh moved the reading position to %d", tab.Scroll)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	sweepSidebarDocumentWatch(t, model)
	if strings.TrimSpace(tab.View.Err) == "" {
		t.Fatalf("removed file kept content %q", tab.View.Text)
	}
	width := model.sidebarTabStripWidth()
	if painted := ansi.Strip(model.renderSidebarDocument(width, 20)); !strings.Contains(painted, "File not found") {
		t.Fatalf("removed preview =\n%s", painted)
	}

	if err := os.WriteFile(path, []byte("# Three\n\nrestored\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	bumpSidebarDocumentModTime(t, path)
	sweepSidebarDocumentWatch(t, model)
	if tab.View.Err != "" || !strings.Contains(tab.View.Text, "restored") {
		t.Fatalf("recovered preview = %q (err %q)", tab.View.Text, tab.View.Err)
	}
	if tab.Scroll != 2 {
		t.Fatalf("recovery moved the reading position to %d", tab.Scroll)
	}
}

// bumpSidebarDocumentModTime makes the change visible to the stamp poll even on
// filesystems with a coarse modification-time clock.
func bumpSidebarDocumentModTime(t *testing.T, path string) {
	t.Helper()
	stamp := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
}

// sweepSidebarDocumentWatch runs one refresh sweep without waiting for the
// timer that arms the next one.
func sweepSidebarDocumentWatch(t *testing.T, model *Model) {
	t.Helper()
	for _, cmd := range model.sidebarDocumentReloadChangedCmds() {
		runTeaCmd(t, model, cmd)
	}
}
