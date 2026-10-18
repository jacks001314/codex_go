package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// TestAsyncQuestionNotificationExpiresWhenTurnEndsLikeRust mirrors Rust
// #47424: an old question notification must not outlive its turn and suppress
// the lower-priority completion notification.
func TestAsyncQuestionNotificationExpiresWhenTurnEndsLikeRust(t *testing.T) {
	tests := []struct {
		name       string
		finish     func(*Model) bubbletea.Cmd
		wantPosted []string
	}{
		{
			name: "completed",
			finish: func(model *Model) bubbletea.Cmd {
				_, cmd := model.Update(TurnCompletedMsg{ThreadID: "thread", AssistantMessage: "done"})
				return cmd
			},
			wantPosted: []string{"done"},
		},
		{
			name: "interrupted",
			finish: func(model *Model) bubbletea.Cmd {
				_, cmd := model.Update(TurnInterruptedMsg{})
				return cmd
			},
		},
		{
			name: "failed",
			finish: func(model *Model) bubbletea.Cmd {
				_, cmd := model.Update(TurnCompletedMsg{ThreadID: "thread", Err: errors.New("Turn failed")})
				return cmd
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var posted []string
			state := codextui.NewState(nil)
			state.SetThreadID("thread")
			state.SetStatus("running")
			model := NewModel(state, Options{
				Width:                 120,
				Height:                40,
				NotificationCondition: codextui.NotificationConditionAlways,
				OnPostNotification: func(message string, _ codextui.NotificationMethod) bubbletea.Cmd {
					return func() bubbletea.Msg {
						posted = append(posted, message)
						return nil
					}
				},
			})
			model = feedAsyncQuestions(t, model, "question", []any{
				map[string]any{"title": "Which way?"},
			})
			pending := chatwidget.AsyncQuestionNotification("Which way?")
			model.notifications.Pending = &pending

			cmd := testCase.finish(model)
			runTeaCmd(t, model, cmd)

			if model.notifications.Pending != nil {
				t.Fatalf("pending notification = %#v, want nil", model.notifications.Pending)
			}
			if model.asyncQuestions.UnansweredCount() != 0 {
				t.Fatalf("unanswered questions = %d, want 0", model.asyncQuestions.UnansweredCount())
			}
			if strings.Join(posted, "\n") != strings.Join(testCase.wantPosted, "\n") {
				t.Fatalf("posted notifications = %#v, want %#v", posted, testCase.wantPosted)
			}
		})
	}
}

// TestTurnEndKeepsHigherPriorityNotificationLikeRust verifies that only an
// AsyncQuestion notification is discarded at turn end.
func TestTurnEndKeepsHigherPriorityNotificationLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread")
	model := NewModel(state, Options{Width: 120, Height: 40})
	pending := chatwidget.ExecApprovalRequestedNotification("go test ./...")
	model.notifications.Pending = &pending

	model.Update(TurnCompletedMsg{ThreadID: "thread", AssistantMessage: "done"})

	if model.notifications.Pending == nil || model.notifications.Pending.Kind != chatwidget.NotificationExecApprovalRequested {
		t.Fatalf("pending notification = %#v, want approval request", model.notifications.Pending)
	}
}

// TestTurnEndRecoversAsyncQuestionDraftsLikeRust mirrors Rust's turn-end draft
// recovery: both open answers are appended in order, the main draft and its
// text element survive, and the questions are consumed exactly once.
func TestTurnEndRecoversAsyncQuestionDraftsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread")
	state.SetStatus("running")
	model := NewModel(state, Options{Width: 120, Height: 40})
	mainDraft := "$tool existing draft"
	model.composer.SetValue(mainDraft)
	model.registerComposerElement(mainDraft, 0, len("$tool"))
	model = feedAsyncQuestions(t, model, "question", []any{
		map[string]any{"title": "Which style?"},
		map[string]any{"title": "What budget?"},
	})

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	model.composer.SetValue("  Over-ear  ")
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	model.composer.SetValue("Under 250")

	model.Update(TurnCompletedMsg{ThreadID: "thread", AssistantMessage: "done"})

	want := "$tool existing draft\n\n> Which style?\n\nOver-ear\n\n> What budget?\n\nUnder 250"
	if got := model.composerText(); got != want {
		t.Fatalf("composer = %q, want %q", got, want)
	}
	if model.asyncQuestions.UnansweredCount() != 0 {
		t.Fatalf("unanswered questions = %d, want 0", model.asyncQuestions.UnansweredCount())
	}
	if len(model.composerElements) != 1 ||
		model.composerElements[0].Start != 0 ||
		model.composerElements[0].End != len("$tool") ||
		model.composerElements[0].Placeholder != "$tool" {
		t.Fatalf("composer elements = %#v, want the original $tool element", model.composerElements)
	}

	model.Update(TurnCompletedMsg{ThreadID: "thread", AssistantMessage: "done again"})
	if got := model.composerText(); got != want {
		t.Fatalf("second completion composer = %q, want recovered draft once", got)
	}
}

// TestTurnEndEscapesCommandDraftBeforeRecoveryLikeRust ensures recovered
// question text cannot turn an existing shell or slash draft into command input.
func TestTurnEndEscapesCommandDraftBeforeRecoveryLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		mainDraft string
		wantMain  string
	}{
		{mainDraft: "!echo partial", wantMain: "\\!echo partial"},
		{mainDraft: "/diff", wantMain: "\\/diff"},
		{mainDraft: "/tmp/file", wantMain: "/tmp/file"},
	} {
		t.Run(testCase.mainDraft, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread")
			model := NewModel(state, Options{Width: 120, Height: 40})
			model.setComposerPrompt(testCase.mainDraft)
			model = feedAsyncQuestions(t, model, "question", []any{
				map[string]any{"title": "Which way?"},
			})
			updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
			model = updated.(*Model)
			model.composer.SetValue("answer")

			model.Update(TurnCompletedMsg{ThreadID: "thread", AssistantMessage: "done"})

			want := testCase.wantMain + "\n\n> Which way?\n\nanswer"
			if got := model.composerText(); got != want {
				t.Fatalf("composer = %q, want %q", got, want)
			}
		})
	}
}

// TestThreadTurnEndClearsAsyncQuestionNotificationLikeRust covers live
// protocol completion and failure events, which must not leave stale question
// notifications behind either.
func TestThreadTurnEndClearsAsyncQuestionNotificationLikeRust(t *testing.T) {
	for _, eventType := range []string{"turn.completed", "turn.failed"} {
		t.Run(eventType, func(t *testing.T) {
			state := codextui.NewState(nil)
			state.SetThreadID("thread")
			model := NewModel(state, Options{Width: 120, Height: 40})
			model = feedAsyncQuestions(t, model, "question", []any{
				map[string]any{"title": "Which way?"},
			})
			pending := chatwidget.AsyncQuestionNotification("Which way?")
			model.notifications.Pending = &pending
			event := protocol.ThreadEvent{Type: eventType}
			if eventType == "turn.failed" {
				event.Error = &protocol.ThreadError{Message: "Turn failed"}
			}

			model.applyThreadEvent(event)

			if model.notifications.Pending != nil {
				t.Fatalf("pending notification = %#v, want nil", model.notifications.Pending)
			}
			if model.asyncQuestions.UnansweredCount() != 0 {
				t.Fatalf("unanswered questions = %d, want 0", model.asyncQuestions.UnansweredCount())
			}
		})
	}
}
