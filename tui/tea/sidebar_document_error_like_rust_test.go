package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust #62362: a failed preview names the file and its directory, classifies
// missing files and permission errors, and keeps other failures' detail.
func TestSidebarDocumentOpenErrorNamesPathLikeRust(t *testing.T) {
	view := SidebarDocumentView{
		Path: "/workspace/docs/notes.md",
		Err:  "open /workspace/docs/notes.md: no such file or directory",
	}
	tab := &sidebarDocumentTab{Path: view.Path, View: view}
	plain := ansi.Strip(strings.Join(sidebarDocumentOpenErrorLines(tab, view, 60), "\n"))
	for _, want := range []string{"File not found", "notes.md", "in /workspace/docs/", "This file may have moved since the link was added."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing-file preview = %q, want %q", plain, want)
		}
	}

	view.Err = "fs/readFile: Permission denied"
	plain = ansi.Strip(strings.Join(sidebarDocumentOpenErrorLines(tab, view, 60), "\n"))
	for _, want := range []string{"Permission denied", "notes.md", "Check your read access, then reopen the link."} {
		if !strings.Contains(plain, want) {
			t.Fatalf("permission preview = %q, want %q", plain, want)
		}
	}

	view.Err = "Connection changed. Reopen this file to try again."
	plain = ansi.Strip(strings.Join(sidebarDocumentOpenErrorLines(tab, view, 60), "\n"))
	if !strings.Contains(plain, "Could not open file") || !strings.Contains(plain, view.Err) {
		t.Fatalf("other failure preview = %q, want the retained detail", plain)
	}
}

// The attempted path stays readable in a narrow pane: the filename and the
// directory wrap instead of being truncated by the pane width.
func TestSidebarDocumentOpenErrorWrapsNarrowPaneLikeRust(t *testing.T) {
	view := SidebarDocumentView{
		Path: "/workspace/some/deeply/nested/directory/notes-with-a-long-name.md",
		Err:  "The system cannot find the file specified.",
	}
	tab := &sidebarDocumentTab{Path: view.Path, View: view}
	const width = 14
	lines := sidebarDocumentOpenErrorLines(tab, view, width)
	if len(lines) < 4 {
		t.Fatalf("narrow preview lines = %d, want the wrapped path", len(lines))
	}
	plain := make([]string, len(lines))
	for index, line := range lines {
		plain[index] = ansi.Strip(line)
		if got := codextui.DisplayWidth(plain[index]); got > width {
			t.Fatalf("line %q width = %d, want <= %d", plain[index], got, width)
		}
	}
	joined := strings.Join(plain, "\n")
	// The long filename and the directory both wrap instead of losing their
	// tails; the fragments below prove every part reaches the pane.
	for _, want := range []string{"File not found", "notes-with-a-l", "ong-name.md", "in /workspace/", "This file may"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("narrow preview = %q, want %q", joined, want)
		}
	}
}
