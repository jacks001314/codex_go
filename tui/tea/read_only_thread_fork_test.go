package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

// Mirrors Rust's external_writer_fork_tests: f/F (plain or shifted) forks the
// locked thread into an editable session without taking the source lease, the
// notice advertises it, and other modifier combinations do not.
func TestReadOnlyViewForkShortcutMatchesRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	var actions []codextui.SessionSelection
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			return &codextui.SessionSummary{ThreadID: "thread-forked", Title: "Forked"}, nil
		},
		OnModalResponse: func(ModalResponse) bubbletea.Cmd { return nil },
	})
	model.applyResumeResponse("thread-source", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-source"},
		ReadOnly: true,
	})
	if !model.readOnlyThread {
		t.Fatal("the model must enter the read-only state")
	}
	if notice := model.renderReadOnlyThreadNotice(); !strings.Contains(notice, "f fork") {
		t.Fatalf("the notice does not advertise the fork shortcut: %q", notice)
	}

	for _, testCase := range []struct {
		name    string
		message keymsg.KeyMsg
		want    bool
	}{
		{name: "plain f", message: keyRunes('f'), want: true},
		{name: "shifted F", message: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("F")}, want: true},
		{name: "alt f", message: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("f"), Alt: true}, want: false},
		{name: "shift-alt f", message: keymsg.KeyMsg{Type: keymsg.KeyRunes, Runes: []rune("F"), Alt: true}, want: false},
		{name: "ctrl f", message: keymsg.KeyMsg{Type: keymsg.KeyCtrlF}, want: false},
	} {
		actions = nil
		state.SetThreadID("thread-source")
		// Rust #47185: a successful fork leaves the locked view for the new
		// editable conversation, so each shortcut case starts locked again.
		model.setReadOnlyThread(true)
		updated, cmd := model.Update(testCase.message)
		model = updated.(*Model)
		// The locked-view fork defers the RPC to a command; without draining it
		// no request is issued at all.
		if testCase.want {
			model = runLockedForkCmd(t, model, cmd)
		}
		forked := len(actions) == 1 && actions[0].Kind == codextui.SessionSelectionFork && actions[0].Target.ThreadID == "thread-source"
		if forked != testCase.want {
			t.Fatalf("%s: fork selections = %#v, want forked=%v", testCase.name, actions, testCase.want)
		}
		if testCase.want && state.ThreadID != "thread-forked" {
			t.Fatalf("%s: thread = %q, want the forked session", testCase.name, state.ThreadID)
		}
	}
}

// The fork shortcut belongs to the read-only notice; an overlay or modal owns
// the input first, matching Rust's suppression while another view is active.
func TestReadOnlyViewForkShortcutYieldsToOpenViews(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	forks := 0
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(codextui.SessionSelection) (*codextui.SessionSummary, error) {
			forks++
			return &codextui.SessionSummary{ThreadID: "thread-forked"}, nil
		},
	})
	model.applyResumeResponse("thread-source", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-source"},
		ReadOnly: true,
	})
	model.openModal(ModalRequestMsg{ID: "blocking", Kind: ModalKindAgents, Title: "Blocking", Options: []ModalOption{{ID: "ok", Label: "OK"}}})
	model.Update(keyRunes('f'))
	if forks != 0 {
		t.Fatalf("a blocking modal must own the fork key: forks = %d", forks)
	}
}

// Rust #47185 restores the locked thread's whole ThreadInputState onto the fork
// (external_writer_fork_tests.rs:156-182, chatwidget/input_restore.rs:607-654),
// so the queued prompt, the draft and the composer's image, and a steer that
// had not committed yet all continue in the editable fork.
func TestLockedForkRetainsQueuedPromptDraftImageAndSteersLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(codextui.SessionSelection) (*codextui.SessionSummary, error) {
			return &codextui.SessionSummary{ThreadID: "thread-forked"}, nil
		},
		OnModalResponse: func(ModalResponse) bubbletea.Cmd { return nil },
	})
	// The draft, its image and the queue exist before the conversation locks,
	// mirroring Rust's suppressed-autosend setup.
	model.composer.SetValue("Retained draft")
	model.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: `C:\shots\retained.png`}}
	model.queued = []queuedSubmission{{Request: SubmitRequest{Prompt: "Retained queued prompt"}}}
	model.applyResumeResponse("thread-source", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-source"},
		ReadOnly: true,
	})
	if !model.externalWriterView() {
		t.Fatal("the resume response did not enter the external-writer view")
	}
	model.pendingSteers = []pendingSteerSubmission{{ID: "client-steer", Request: SubmitRequest{Prompt: "Retained steer"}}}

	updated, cmd := model.Update(keyRunes('f'))
	model = updated.(*Model)
	if !model.forkInProgress {
		t.Fatal("the locked fork did not paint its progress state")
	}
	model = runLockedForkCmd(t, model, cmd)

	if state.ThreadID != "thread-forked" {
		t.Fatalf("thread after the locked-view fork = %q, want the fork", state.ThreadID)
	}
	if model.readOnlyThread {
		t.Fatal("the fork is editable, so the locked view must be gone")
	}
	if got := model.composerBody(); got != "Retained draft" {
		t.Fatalf("draft after fork = %q, want it retained", got)
	}
	if len(model.attachments) != 1 || model.attachments[0].Path != `C:\shots\retained.png` {
		t.Fatalf("draft image after fork = %#v, want it retained", model.attachments)
	}
	if len(model.queued) != 1 || model.queued[0].Request.Prompt != "Retained queued prompt" {
		t.Fatalf("queued prompts after fork = %#v, want them retained", model.queued)
	}
	if len(model.pendingSteers) != 1 || model.pendingSteers[0].ID != "client-steer" {
		t.Fatalf("pending steers after fork = %#v, want the uncommitted steer on the fork", model.pendingSteers)
	}
	if model.pendingSteersThreadID != "thread-forked" {
		t.Fatalf("pending steer owner = %q, want the fork", model.pendingSteersThreadID)
	}
	if _, parked := model.threadPendingSteers["thread-source"]; parked {
		t.Fatal("the source thread kept a copy of the steer and would send it twice")
	}

	view := model.viewString()
	plainView := utils.StripANSI(view)
	for _, want := range []string{"Retained draft", "Retained queued prompt", "Fork created. You can continue here."} {
		if !strings.Contains(plainView, want) {
			t.Fatalf("fork view missing %q:\n%s", want, view)
		}
	}
	if !strings.Contains(utils.StripANSI(view), "retained.png") {
		t.Fatalf("fork view lost the draft image:\n%s", view)
	}
}

// A failed fork leaves the locked view, the draft and its image untouched
// (external_writer_fork_tests.rs:232-277).
func TestLockedForkFailureKeepsDraftImageLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-source")
	model := NewModel(state, Options{
		Width:  120,
		Height: 40,
		OnSessionAction: func(codextui.SessionSelection) (*codextui.SessionSummary, error) {
			return nil, errors.New("thread is still locked")
		},
		OnModalResponse: func(ModalResponse) bubbletea.Cmd { return nil },
	})
	model.composer.SetValue("Retained draft")
	model.attachments = []bottompane.ComposerAttachment{{Kind: bottompane.AttachmentImage, Path: `C:\shots\retained.png`}}
	model.applyResumeResponse("thread-source", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-source"},
		ReadOnly: true,
	})
	updated, cmd := model.Update(keyRunes('f'))
	model = updated.(*Model)
	model = runLockedForkCmd(t, model, cmd)
	if state.ThreadID != "thread-source" || !model.externalWriterView() {
		t.Fatalf("failed fork changed the view: thread=%q readOnly=%v", state.ThreadID, model.readOnlyThread)
	}
	if model.forkInProgress {
		t.Fatal("the fork guard must clear after a rejected fork")
	}
	model.setReadOnlyThread(false)
	if got := model.composerBody(); got != "Retained draft" {
		t.Fatalf("draft after a failed fork = %q", got)
	}
	if len(model.attachments) != 1 || model.attachments[0].Path != `C:\shots\retained.png` {
		t.Fatalf("draft image after a failed fork = %#v", model.attachments)
	}
}
