package tea

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

var ansiEscapePattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func plainTeaText(value string) string {
	return ansiEscapePattern.ReplaceAllString(value, "")
}

// TestArchivedSessionGuidanceLikeRust mirrors Rust
// session_start_tests::session_start_error_surfaces_archived_guidance_without_rollout_path:
// the guidance is extracted from a resume/fork failure with or without a
// trailing JSON-RPC code, and unrelated failures never match.
func TestArchivedSessionGuidanceLikeRust(t *testing.T) {
	threadID := "019e72f4-e09a-70f2-b2c2-a153a57b8cc0"
	expected := "session " + threadID + " is archived. Run `codex unarchive " + threadID + "` to unarchive it first."
	for _, verb := range []string{"resume", "fork"} {
		err := errors.New("thread/" + verb + " failed during TUI bootstrap: thread/" + verb + " failed: " + expected + " (code -32600)")
		guidance, ok := archivedSessionGuidance(err)
		if !ok || guidance != expected {
			t.Fatalf("%s guidance = %q ok=%v, want %q", verb, guidance, ok, expected)
		}
		if _, ok := archivedSessionGuidanceForThread(err, threadID); !ok {
			t.Fatalf("%s guidance did not match the requested thread", verb)
		}
	}
	for name, err := range map[string]error{
		"nil":              nil,
		"unrelated":        errors.New("thread/resume failed during TUI bootstrap: no rollout found"),
		"different thread": errors.New("session 11111111-1111-1111-1111-111111111111 is archived. Run `codex unarchive 11111111-1111-1111-1111-111111111111` to unarchive it first."),
	} {
		if _, ok := archivedSessionGuidance(err); ok && name != "different thread" {
			t.Fatalf("%s: archivedSessionGuidance matched %v", name, err)
		}
		if _, ok := archivedSessionGuidanceForThread(err, threadID); ok {
			t.Fatalf("%s: archivedSessionGuidanceForThread matched %v", name, err)
		}
	}
}

// TestUnarchivePromptRenderLikeRust mirrors Rust unarchive_prompt_tests
// (resume_prompt_snapshot / fork_prompt_cancel_snapshot).
func TestUnarchivePromptRenderLikeRust(t *testing.T) {
	threadID := "019e72f4-e09a-70f2-b2c2-a153a57b8cc0"
	resume := newUnarchivePromptState(threadID, "resume")
	wantResume := strings.Join([]string{
		"This conversation is archived",
		threadID,
		"",
		"› 1. Unarchive and resume",
		"  2. Cancel",
		"",
		"Press enter to continue or esc to cancel",
	}, "\n")
	if got := plainTeaText(resume.render()); got != wantResume {
		t.Fatalf("resume prompt =\n%s\nwant\n%s", got, wantResume)
	}
	fork := newUnarchivePromptState(threadID, "fork")
	if choice, done := fork.handleKey(key(bubbletea.KeyDown)); done || choice != 0 {
		t.Fatalf("down = %v done=%v, want highlight toggle", choice, done)
	}
	wantFork := strings.Join([]string{
		"This conversation is archived",
		threadID,
		"",
		"  1. Unarchive and fork",
		"› 2. Cancel",
		"",
		"Press enter to continue or esc to cancel",
	}, "\n")
	if got := plainTeaText(fork.render()); got != wantFork {
		t.Fatalf("fork prompt =\n%s\nwant\n%s", got, wantFork)
	}
	if choice, done := fork.handleKey(key(bubbletea.KeyEnter)); !done || choice != unarchiveChoiceCancel {
		t.Fatalf("enter on highlighted cancel = %v done=%v", choice, done)
	}
}

// TestUnarchivePromptKeysLikeRust mirrors Rust
// unarchive_prompt_tests::confirmation_requires_an_accept_key.
func TestUnarchivePromptKeysLikeRust(t *testing.T) {
	prompt := newUnarchivePromptState("thread-1", "fork")
	for _, message := range []bubbletea.KeyMsg{
		key(bubbletea.KeyEsc),
		keyRunes('n'),
		keyRunes('2'),
	} {
		if choice, done := prompt.handleKey(message); !done || choice != unarchiveChoiceCancel {
			t.Fatalf("cancel key %v = %v done=%v", message, choice, done)
		}
	}
	for _, r := range []rune{'c', 'd'} {
		message := bubbletea.KeyMsg{Type: bubbletea.KeyCtrlC}
		if r == 'd' {
			message.Type = bubbletea.KeyCtrlD
		}
		if choice, done := prompt.handleKey(message); !done || choice != unarchiveChoiceQuit {
			t.Fatalf("ctrl+%c = %v done=%v", r, choice, done)
		}
	}
	if choice, done := prompt.handleKey(keyRunes('y')); !done || choice != unarchiveChoiceUnarchive {
		t.Fatalf("y = %v done=%v", choice, done)
	}
	if choice, done := prompt.handleKey(key(bubbletea.KeyEnter)); !done || choice != unarchiveChoiceUnarchive {
		t.Fatalf("enter = %v done=%v", choice, done)
	}
}

// TestModelArchivedResumePromptsToUnarchiveAndRetriesLikeRust covers Rust
// session_start_tests::archived_session_requires_confirmation_before_resume_or_fork:
// the resume fails with the archived guidance, the prompt appears, and
// confirming unarchives and retries the resume.
func TestModelArchivedResumePromptsToUnarchiveAndRetriesLikeRust(t *testing.T) {
	now := fixedTeaTime()
	threadID := "019e72f4-e09a-70f2-b2c2-a153a57b8cc0"
	archived := true
	resumeCalls := 0
	var actions []codextui.SessionSelection
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		SessionPickerCWD: `D:\repo\a`,
		SessionPickerItems: []codextui.SessionSummary{{
			ThreadID:  threadID,
			Title:     "Archived Session",
			CWD:       `D:\repo\a`,
			UpdatedAt: now,
		}},
		OnSessionAction: func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
			actions = append(actions, selection)
			if selection.Kind == codextui.SessionSelectionUnarchive {
				archived = false
			}
			return &codextui.SessionSummary{ThreadID: threadID, Title: "Archived Session"}, nil
		},
		OnResumeSession: func(selection codextui.SessionSelection) (SessionResumeResponse, error) {
			resumeCalls++
			if archived {
				return SessionResumeResponse{}, errors.New("thread/resume failed during TUI bootstrap: session " + threadID +
					" is archived. Run `codex unarchive " + threadID + "` to unarchive it first. (code -32600)")
			}
			return SessionResumeResponse{
				Summary:  &codextui.SessionSummary{ThreadID: threadID, Title: "Archived Session"},
				Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "restored prompt"}},
				Status:   "idle",
			}, nil
		},
	})
	model.now = func() time.Time { return now }

	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter)) // open the picker
	model.Update(key(bubbletea.KeyEnter)) // resume the archived session
	if resumeCalls != 1 {
		t.Fatalf("resume calls = %d, want 1", resumeCalls)
	}
	view := plainTeaText(model.View())
	for _, want := range []string{
		"This conversation is archived",
		threadID,
		"Unarchive and resume",
		"Cancel",
		"Press enter to continue or esc to cancel",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("unarchive prompt missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Resume failed") {
		t.Fatalf("archived resume should prompt instead of failing:\n%s", view)
	}

	_, cmd := model.Update(keyRunes('1'))
	runTeaCmd(t, model, cmd)
	if len(actions) != 1 || actions[0].Kind != codextui.SessionSelectionUnarchive || actions[0].Target.ThreadID != threadID {
		t.Fatalf("session actions = %#v", actions)
	}
	if resumeCalls != 2 {
		t.Fatalf("resume retry calls = %d, want 2", resumeCalls)
	}
	if state.ThreadID != threadID {
		t.Fatalf("ThreadID = %q, want %q", state.ThreadID, threadID)
	}
	if model.modal != nil {
		t.Fatalf("prompt stayed open after unarchive: %#v", model.modal)
	}
}
