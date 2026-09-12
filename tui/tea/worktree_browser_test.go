package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	"codex_go/worktree"
)

func newWorktreeModel(t *testing.T, options Options) *Model {
	t.Helper()
	if options.WorktreeRepositoryAvailable == nil {
		options.WorktreeRepositoryAvailable = func(string) bool { return true }
	}
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	state.CWD = `D:\repo`
	return NewModel(state, options)
}

func worktreeChoices(t *testing.T, model *Model) []string {
	t.Helper()
	browser := model.activeWorktreeBrowser()
	if browser == nil {
		t.Fatal("worktree popup is not open")
	}
	labels := make([]string, 0, len(browser.Choices))
	for _, choice := range browser.Choices {
		labels = append(labels, choice.Label)
	}
	return labels
}

func modelMessageText(model *Model) string {
	if model == nil || model.State == nil {
		return ""
	}
	texts := make([]string, 0, len(model.State.Messages))
	for _, message := range model.State.Messages {
		texts = append(texts, message.Text)
	}
	return strings.Join(texts, "\n")
}

// TestWorktreeCommandAvailability mirrors Rust's `/worktree` gating: the
// command needs the worktrees feature, local worktree operations, and a Git
// repository.
func TestWorktreeCommandAvailability(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	state.CWD = `D:\repo`

	disabled := NewModel(codextui.NewState(nil), Options{})
	if cmd := disabled.applyWorktreeCommand(""); cmd != nil {
		t.Fatal("disabled worktrees should not open a popup")
	}
	if !strings.Contains(modelMessageText(disabled), "Enable worktrees in your Codex configuration") {
		t.Fatalf("feature-disabled notice missing: %q", modelMessageText(disabled))
	}

	remote := NewModel(state, Options{WorktreesEnabled: true})
	remote.applyWorktreeCommand("")
	if remote.notice != "Unrecognized command '/worktree'" {
		t.Fatalf("local-operations notice = %q", remote.notice)
	}

	nonRepo := newWorktreeModel(t, Options{
		WorktreesEnabled:            true,
		LocalWorktreeOperations:     true,
		WorktreeRepositoryAvailable: func(string) bool { return false },
	})
	nonRepo.applyWorktreeCommand("")
	if nonRepo.modal != nil {
		t.Fatal("non-repository should not open the popup")
	}
	if !strings.Contains(modelMessageText(nonRepo), "Managed worktrees require a local Git repository.") {
		t.Fatalf("non-repository notice missing: %q", modelMessageText(nonRepo))
	}

	available := newWorktreeModel(t, Options{
		WorktreesEnabled:        true,
		LocalWorktreeOperations: true,
	})
	available.applyWorktreeCommand("")
	labels := worktreeChoices(t, available)
	want := []string{"Continue current conversation", "Start new conversation", "Browse worktrees"}
	if len(labels) != len(want) {
		t.Fatalf("chooser labels = %#v", labels)
	}
	for index := range want {
		if labels[index] != want[index] {
			t.Fatalf("chooser labels = %#v, want %#v", labels, want)
		}
	}
}

// TestWorktreeCommandStartsManagedSession covers the creation choices: the
// chooser runs the app handler and attaches the returned session (Rust #43120).
func TestWorktreeCommandStartsManagedSession(t *testing.T) {
	var modes []string
	model := newWorktreeModel(t, Options{
		WorktreesEnabled:        true,
		LocalWorktreeOperations: true,
		OnStartManagedWorktree: func(mode string, name string, cwd string, threadID string) (SessionResumeResponse, error) {
			modes = append(modes, mode+"@"+cwd+"/"+threadID)
			return SessionResumeResponse{
				Summary: &codextui.SessionSummary{ThreadID: "thread-worktree", CWD: `D:\repo\wt`},
			}, nil
		},
	})
	model.applyWorktreeCommand("")
	if _, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter}); cmd != nil {
		_ = cmd
	}
	if len(modes) != 1 || modes[0] != "fork@D:\\repo/thread-main" {
		t.Fatalf("creation calls = %#v", modes)
	}
	if model.modal != nil {
		t.Fatal("creation must dismiss the chooser")
	}
	if model.State.ThreadID != "thread-worktree" {
		t.Fatalf("thread = %q, want the created session", model.State.ThreadID)
	}
}

// TestWorktreeBrowserLoadsFiltersAndGatesStaleResults covers the browse flow:
// loading, owner-aware rows, search filtering, and stale-result rejection
// (Rust #43286).
func TestWorktreeBrowserLoadsFiltersAndGatesStaleResults(t *testing.T) {
	model := newWorktreeModel(t, Options{
		WorktreesEnabled:        true,
		LocalWorktreeOperations: true,
		WorktreeSettings:        worktree.WorktreeSettings{},
	})
	model.applyWorktreeCommand("")
	model.moveWorktreeSelection(model.activeWorktreeBrowser(), 2, 3)
	if _, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter}); cmd == nil {
		t.Fatal("browse should schedule the listing")
	}
	browser := model.activeWorktreeBrowser()
	if browser == nil || browser.View != worktreeViewLoading {
		t.Fatalf("browser = %#v, want the loading view", browser)
	}
	request := browser.Request
	if request.ID == "" || request.CWD != `D:\repo` || request.ThreadID != "thread-main" {
		t.Fatalf("request = %#v", request)
	}

	// A stale request (different ID) must be ignored.
	model.Update(WorktreeBrowserLoadedMsg{
		Request: codextui.WorktreeBrowserRequest{ID: "stale", CWD: request.CWD, ThreadID: request.ThreadID},
		Entries: []codextui.WorktreeBrowserEntry{{Root: `D:\repo\wt`, CWD: `D:\repo\wt`}},
	})
	if browser.View != worktreeViewLoading {
		t.Fatalf("stale listing applied: %#v", browser)
	}

	entries := []codextui.WorktreeBrowserEntry{
		{
			Root: `D:\repo\wt-a`,
			CWD:  `D:\repo\wt-a`,
			Owner: codextui.WorktreeOwner{
				Kind:     codextui.WorktreeOwnerResumable,
				ThreadID: "owner-a",
				Summary:  &codextui.WorktreeThreadSummary{ID: "owner-a", Title: "Database work", UpdatedAt: model.currentTime().Unix() - 120},
			},
		},
		{Root: `D:\repo\wt-b`, CWD: `D:\repo\wt-b`, Owner: codextui.WorktreeOwner{Kind: codextui.WorktreeOwnerNone}},
	}
	model.Update(WorktreeBrowserLoadedMsg{Request: request, Entries: entries})
	if browser.View != worktreeViewList || len(browser.Entries) != 2 {
		t.Fatalf("browser = %#v, want the list view", browser)
	}
	view := model.View()
	if !strings.Contains(view, "Database work") || !strings.Contains(view, "No attached thread") {
		t.Fatalf("list rows missing:\n%s", view)
	}

	// Search filters on the owner title and working directory.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("wt-b")})
	if indices := browser.filteredWorktreeIndices(model.currentTime().Unix()); len(indices) != 1 || indices[0] != 1 {
		t.Fatalf("filtered indices = %#v", indices)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyBackspace})
	if indices := browser.filteredWorktreeIndices(model.currentTime().Unix()); len(indices) != 2 {
		t.Fatalf("cleared filter indices = %#v", indices)
	}
}

// TestWorktreeBrowserActionsAndRemoval covers the action list, the resume/copy
// dispatch, and the delete confirmation plus removal result (Rust #43286/#43942).
func TestWorktreeBrowserActionsAndRemoval(t *testing.T) {
	var resumed []string
	var copied []string
	model := newWorktreeModel(t, Options{
		WorktreesEnabled:        true,
		LocalWorktreeOperations: true,
		OnResumeSession: func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
			resumed = append(resumed, selection.Target.ThreadID)
			return SessionResumeResponse{}, nil
		},
		OnClipboardWrite: func(text string) error {
			copied = append(copied, text)
			return nil
		},
	})
	model.applyWorktreeCommand("")
	model.moveWorktreeSelection(model.activeWorktreeBrowser(), 2, 3)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	request := model.activeWorktreeBrowser().Request
	model.Update(WorktreeBrowserLoadedMsg{Request: request, Entries: []codextui.WorktreeBrowserEntry{
		{
			Root: `D:\repo\wt-a`,
			CWD:  `D:\repo\wt-a`,
			Owner: codextui.WorktreeOwner{
				Kind:     codextui.WorktreeOwnerResumable,
				ThreadID: "owner-a",
				Summary:  &codextui.WorktreeThreadSummary{ID: "owner-a", Title: "Database work"},
			},
		},
	}})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	browser := model.activeWorktreeBrowser()
	if browser == nil || browser.View != worktreeViewActions || len(browser.Actions) != 3 {
		t.Fatalf("actions = %#v", browser)
	}

	// Resume the owner thread.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if len(resumed) != 1 || resumed[0] != "owner-a" {
		t.Fatalf("resumed = %#v", resumed)
	}
	if model.activeWorktreeBrowser() != nil {
		t.Fatal("resume must dismiss the popup")
	}

	// Copy the working directory.
	model.applyWorktreeCommand("")
	model.moveWorktreeSelection(model.activeWorktreeBrowser(), 2, 3)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	request = model.activeWorktreeBrowser().Request
	model.Update(WorktreeBrowserLoadedMsg{Request: request, Entries: []codextui.WorktreeBrowserEntry{{Root: `D:\repo\wt-b`, CWD: `D:\repo\wt-b`}}})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if len(copied) != 1 || copied[0] != `D:\repo\wt-b` {
		t.Fatalf("copied = %#v", copied)
	}

	// Deleting the checkout the session lives in is refused, but a sibling
	// worktree can be deleted after confirmation.
	model.applyWorktreeCommand("")
	model.moveWorktreeSelection(model.activeWorktreeBrowser(), 2, 3)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	request = model.activeWorktreeBrowser().Request
	model.Update(WorktreeBrowserLoadedMsg{Request: request, Entries: []codextui.WorktreeBrowserEntry{{Root: `D:\repo`, CWD: `D:\repo`}}})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	browser = model.activeWorktreeBrowser()
	deleteItem := -1
	for index, item := range browser.Actions {
		if item.Action.Kind == codextui.WorktreeActionRemove {
			deleteItem = index
		}
	}
	if deleteItem < 0 || !browser.Actions[deleteItem].Disabled {
		t.Fatalf("current checkout delete should be disabled: %#v", browser.Actions)
	}

	// A sibling worktree can be deleted after confirmation.
	model.applyWorktreeCommand("")
	model.moveWorktreeSelection(model.activeWorktreeBrowser(), 2, 3)
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	request = model.activeWorktreeBrowser().Request
	model.Update(WorktreeBrowserLoadedMsg{Request: request, Entries: []codextui.WorktreeBrowserEntry{{Root: `D:\repo\wt-c`, CWD: `D:\repo\wt-c`}}})
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	browser = model.activeWorktreeBrowser()
	for index, item := range browser.Actions {
		if item.Action.Kind == codextui.WorktreeActionRemove {
			browser.Selected = index
		}
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if browser := model.activeWorktreeBrowser(); browser == nil || browser.View != worktreeViewConfirm {
		t.Fatalf("confirm view = %#v", browser)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyDown})
	_, cmd := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirming deletion should schedule removal")
	}
	if model.activeWorktreeBrowser() != nil {
		t.Fatal("confirming deletion must dismiss the popup")
	}
	model.Update(WorktreeBrowserRemovedMsg{Root: `D:\repo\wt-c`})
	if !strings.Contains(model.notice, "Removed worktree at D:\\repo\\wt-c. Thread history was kept.") {
		t.Fatalf("removal notice = %q", model.notice)
	}
}
