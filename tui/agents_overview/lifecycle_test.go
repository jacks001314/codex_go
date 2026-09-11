package agentsoverview

import (
	"strings"
	"testing"
)

func TestArchiveAndDeleteSelectedLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	if action := view.ArchiveSelected(); action != ActionArchiveThread {
		t.Fatalf("ArchiveSelected() = %v, want ActionArchiveThread", action)
	}
	if action := view.DeleteSelected(); action != ActionDeleteThread {
		t.Fatalf("DeleteSelected() = %v, want ActionDeleteThread", action)
	}
	// Archive/delete apply to any selected row, not only active ones.
	view.Selected = 1 // beta (ready)
	if action := view.ArchiveSelected(); action != ActionArchiveThread {
		t.Fatalf("ArchiveSelected() on idle row = %v, want ActionArchiveThread", action)
	}
}

func TestArchiveAndDeleteWithoutRowsAreNoops(t *testing.T) {
	var view View
	if action := view.ArchiveSelected(); action != ActionNone {
		t.Fatalf("ArchiveSelected() on empty view = %v, want ActionNone", action)
	}
	if action := view.DeleteSelected(); action != ActionNone {
		t.Fatalf("DeleteSelected() on empty view = %v, want ActionNone", action)
	}
}

func TestFooterAdvertisesArchiveAndDeleteLikeRust(t *testing.T) {
	view := New(sampleRows(), "", false)
	output := strings.Join(view.Render(200, 24), "\n")
	for _, want := range []string{"ctrl+e archive", "delete delete"} {
		if !strings.Contains(output, want) {
			t.Fatalf("footer missing %q:\n%s", want, output)
		}
	}
	// An unbound action hides its hint.
	view.SetShortcutHint(ShortcutHintDelete, "")
	if hidden := strings.Join(view.Render(200, 24), "\n"); strings.Contains(hidden, "delete delete") {
		t.Fatalf("unbound delete hint still rendered:\n%s", hidden)
	}
}
