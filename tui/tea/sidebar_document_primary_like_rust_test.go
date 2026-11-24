package tea

import (
	"testing"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// Rust #61889 copy-on-select in a document reader publishes PRIMARY and keeps
// the owner on the Selection; Sidebar::cancel_hidden_document_primary clears
// it once the pane stops painting that document.
func TestSidebarDocumentHiddenPrimaryReleasesLikeRust(t *testing.T) {
	model, releases, writes := sidebarDocumentPrimaryModel(t)
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path:       "/workspace/main.go",
		Text:       "alpha beta\ngamma delta\n",
		Code:       true,
		Language:   "go",
		LineStarts: []int{0, 11},
	})
	tab.Selection.start(0, 0)
	tab.Selection.extend(0, 20)
	want := model.sidebarDocumentSelectionText(tab)
	runTeaCmd(t, model, model.finishSidebarDocumentSelection(tab))
	if len(*writes) != 1 || (*writes)[0] != want {
		t.Fatalf("primary writes = %#v, want %q", *writes, want)
	}
	if !tab.primaryPublished || model.primarySelectionDocument != tab.ID {
		t.Fatalf("preview claim published=%t owner=%q tab=%q", tab.primaryPublished, model.primarySelectionDocument, tab.ID)
	}
	if got := *releases; got != 0 {
		t.Fatalf("visible preview released PRIMARY %d times", got)
	}

	// Switching the pane away from the preview hides it.
	model.sidebarDocumentActive = ""
	model.refreshTranscript()
	waitSidebarDocumentPrimaryRelease(t, releases)
	if tab.primaryPublished || model.primarySelectionPublished || model.primarySelectionDocument != "" {
		t.Fatalf("hidden preview kept published=%t owner=%q", tab.primaryPublished, model.primarySelectionDocument)
	}
}

// Closing the preview that owns PRIMARY releases the publication as well.
func TestSidebarDocumentClosedPrimaryReleasesLikeRust(t *testing.T) {
	model, releases, _ := sidebarDocumentPrimaryModel(t)
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path:       "/workspace/main.go",
		Text:       "alpha beta\n",
		Code:       true,
		Language:   "go",
		LineStarts: []int{0},
	})
	tab.Selection.start(0, 0)
	tab.Selection.extend(0, 20)
	runTeaCmd(t, model, model.finishSidebarDocumentSelection(tab))
	if !model.primarySelectionPublished {
		t.Fatal("preview selection did not publish PRIMARY")
	}
	runTeaCmd(t, model, model.closeSidebarDocumentTab(tab.ID))
	waitSidebarDocumentPrimaryRelease(t, releases)
	if model.primarySelectionPublished || model.primarySelectionDocument != "" {
		t.Fatalf("closed preview kept published=%t owner=%q", model.primarySelectionPublished, model.primarySelectionDocument)
	}
}

// When another surface publishes PRIMARY last, hiding the preview must not
// clear that newer selection.
func TestSidebarDocumentHiddenPrimaryLeavesOtherOwnerLikeRust(t *testing.T) {
	model, releases, _ := sidebarDocumentPrimaryModel(t)
	tab := openDocumentTabAt(t, model, SidebarDocumentView{
		Path:       "/workspace/main.go",
		Text:       "alpha beta\n",
		Code:       true,
		Language:   "go",
		LineStarts: []int{0},
	})
	tab.Selection.start(0, 0)
	tab.Selection.extend(0, 20)
	runTeaCmd(t, model, model.finishSidebarDocumentSelection(tab))
	if !tab.primaryPublished {
		t.Fatal("preview selection did not publish PRIMARY")
	}

	// A transcript selection publishes over the preview's value.
	model.applyPrimarySelectionWriteResult(PrimarySelectionWriteResultMsg{Published: true})
	if tab.primaryPublished || model.primarySelectionDocument != "" {
		t.Fatalf("preview kept the claim after another publish: published=%t owner=%q", tab.primaryPublished, model.primarySelectionDocument)
	}
	model.sidebarDocumentActive = ""
	model.refreshTranscript()
	time.Sleep(20 * time.Millisecond)
	if got := *releases; got != 0 {
		t.Fatalf("hiding the preview released another surface's PRIMARY %d times", got)
	}
	if !model.primarySelectionPublished {
		t.Fatal("hiding the preview dropped the newer PRIMARY value")
	}
}

func sidebarDocumentPrimaryModel(t *testing.T) (*Model, *int, *[]string) {
	t.Helper()
	writes := []string{}
	releases := 0
	state := codextui.NewState(nil)
	state.SetThreadID("parent-a")
	state.AddMessage(codextui.RoleUser, "hello from the main chat")
	model := NewModel(state, Options{
		Width:               120,
		Height:              30,
		CopyOnSelect:        "always",
		OnClipboardWrite:    func(string) error { return nil },
		OnPrimarySelectionWrite: func(text string) error {
			writes = append(writes, text)
			return nil
		},
		OnPrimarySelectionRelease: func() error {
			releases++
			return nil
		},
		OnSubmit: func(string) bubbletea.Cmd { return nil },
	})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.refreshTranscript()
	return model, &releases, &writes
}

func waitSidebarDocumentPrimaryRelease(t *testing.T, releases *int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for *releases == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if *releases == 0 {
		t.Fatal("hidden preview did not release PRIMARY ownership")
	}
}
