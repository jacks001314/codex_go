package agentsoverview

import (
	"strings"
	"testing"
)

func composerTestView() *View {
	return New([]Row{
		{ThreadID: "root-1", Name: "alpha", CWD: "/work/a", Group: GroupWorking, StatusType: "active", StatusActive: true},
		{ThreadID: "root-2", Name: "beta", CWD: "/work/a", Group: GroupReady, StatusType: "idle"},
	}, "", false)
}

// Rust #42428: the command center composes in the shared bottom-pane composer.
// Focus decides whether the list or the composer owns input, and Vim insert
// mode keeps Escape for the editor.
func TestAgentsOverviewComposerFocusLikeRust(t *testing.T) {
	view := composerTestView()
	if view.Composing() {
		t.Fatal("the list should own input first")
	}
	view.FocusComposer()
	if !view.Composing() {
		t.Fatal("focusing the composer did not make it own input")
	}
	if view.ComposerOwnsEscape() {
		t.Fatal("Escape should return to the list without Vim")
	}
	if changed := view.SetComposerVimEnabled(true); !changed {
		t.Fatal("enabling Vim did not report a change")
	}
	if view.ComposerVimInsert() != true {
		t.Fatal("enabling Vim should start in insert mode")
	}
	if !view.ComposerOwnsEscape() {
		t.Fatal("Vim insert mode should consume Escape")
	}
	view.SetComposerVimInsert(false)
	if view.ComposerOwnsEscape() {
		t.Fatal("Vim normal mode should not consume Escape")
	}
	// Metadata editors keep input from the composer.
	view.State.Searching = true
	if view.Composing() {
		t.Fatal("search should pause composing")
	}
}

// Rust #42428: drafts and editor state survive dashboard refreshes and
// reconnects because the composer is retained rather than rebuilt.
func TestAgentsOverviewComposerDraftSurvivesRefreshLikeRust(t *testing.T) {
	view := composerTestView()
	view.FocusComposer()
	view.InsertComposerText("draft in progress")
	view.SetComposerCursor(5)
	view.ApplyRefresh([]Row{
		{ThreadID: "root-1", Name: "alpha", CWD: "/work/a", Group: GroupWorking},
		{ThreadID: "root-3", Name: "gamma", CWD: "/work/b", Group: GroupNeedsYou},
	}, "root-1")
	if got := view.ComposerText(); got != "draft in progress" {
		t.Fatalf("draft after refresh = %q", got)
	}
	if got := view.ComposerCursor(); got != 5 {
		t.Fatalf("composer cursor after refresh = %d", got)
	}
	if !view.Composing() {
		t.Fatal("refresh dropped composer focus")
	}
}

// Rust #42428: a multi-line paste stays one draft and submission keeps the
// prompt's whitespace (trim_submission = false).
func TestAgentsOverviewComposerMultilinePasteLikeRust(t *testing.T) {
	view := composerTestView()
	view.FocusComposer()
	if !view.InsertComposerText("first line\r\n  second line  ") {
		t.Fatal("paste was rejected")
	}
	if got := view.ComposerText(); got != "first line\n  second line  " {
		t.Fatalf("pasted draft = %q", got)
	}
	submission, ok := view.TakeComposerSubmission()
	if !ok || submission.Text != "first line\n  second line  " {
		t.Fatalf("submission = %#v ok=%v", submission, ok)
	}
	if !view.ComposerEmpty() {
		t.Fatal("submission left text in the composer")
	}
}

// Rust #42428: an unsent prompt is restored only into an empty composer.
func TestAgentsOverviewRestorePromptLikeRust(t *testing.T) {
	view := composerTestView()
	if !view.RestoreComposerPrompt("  unsent prompt  ") {
		t.Fatal("restore into an empty composer failed")
	}
	if got := view.ComposerText(); got != "  unsent prompt  " {
		t.Fatalf("restored prompt = %q", got)
	}
	if view.RestoreComposerPrompt("other") {
		t.Fatal("restore overwrote a non-empty composer")
	}
}

// Rust #42428: while the composer owns input the dashboard renders it (with
// its placeholder) instead of the browsing search hint.
func TestAgentsOverviewComposerRendersLikeRust(t *testing.T) {
	view := composerTestView()
	view.FocusComposer()
	rendered := strings.Join(view.Render(100, 24), "\n")
	if !strings.Contains(rendered, AgentsOverviewComposerPlaceholder) {
		t.Fatalf("composer placeholder missing:\n%s", rendered)
	}
	if strings.Contains(rendered, "Search tasks") {
		t.Fatalf("composer view still shows the search hint:\n%s", rendered)
	}
	view.InsertComposerText("write tests")
	rendered = strings.Join(view.Render(100, 24), "\n")
	if !strings.Contains(rendered, "write tests") {
		t.Fatalf("composer draft missing:\n%s", rendered)
	}
}
