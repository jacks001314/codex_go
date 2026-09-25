package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
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
		message bubbletea.KeyMsg
		want    bool
	}{
		{name: "plain f", message: keyRunes('f'), want: true},
		{name: "shifted F", message: bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("F")}, want: true},
		{name: "alt f", message: bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("f"), Alt: true}, want: false},
		{name: "shift-alt f", message: bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("F"), Alt: true}, want: false},
		{name: "ctrl f", message: bubbletea.KeyMsg{Type: bubbletea.KeyCtrlF}, want: false},
	} {
		actions = nil
		state.SetThreadID("thread-source")
		model.Update(testCase.message)
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
