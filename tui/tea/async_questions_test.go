package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// asyncQuestionItem builds the async agent-message item the app server emits for
// request_user_input_async (Rust #42178/#42891).
func asyncQuestionItem(id string, questions []any) *protocol.ThreadItem {
	return &protocol.ThreadItem{
		ID:       id,
		Type:     "agent_message",
		Text:     "Which database?",
		Delivery: "async",
		Metadata: map[string]any{"questions": questions},
	}
}

func newAsyncQuestionModel() *Model {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-questions")
	return model
}

func feedAsyncQuestions(t *testing.T, model *Model, id string, questions []any) *Model {
	t.Helper()
	updated, _ := model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
		Type: "item.completed",
		Item: asyncQuestionItem(id, questions),
	}})
	return updated.(*Model)
}

// TestAsyncQuestionsArriveCollapsedWithSummary mirrors Rust's collapsed entry
// point: arriving questions never steal focus, and the bottom pane advertises
// the edit binding that focuses them.
func TestAsyncQuestionsArriveCollapsedWithSummary(t *testing.T) {
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-1", []any{
		map[string]any{"title": "Which database?", "options": []any{"Postgres", "SQLite"}},
		map[string]any{"title": "Deadline?"},
	})

	if model.asyncQuestions.UnansweredCount() != 2 || model.asyncQuestions.Expanded() {
		t.Fatalf("questions = %d expanded=%v", model.asyncQuestions.UnansweredCount(), model.asyncQuestions.Expanded())
	}
	view := model.View()
	if !strings.Contains(view, "2 questions") || !strings.Contains(view, "Alt+Up to answer") {
		t.Fatalf("collapsed summary missing:\n%s", view)
	}
	if strings.Contains(view, "1 of 2") {
		t.Fatalf("collapsed view must not render the editor:\n%s", view)
	}
}

// TestAsyncQuestionsExpandAnswerAndAdvance covers the full answer flow: Alt+Up
// focuses the first question, Enter submits the bounded AnsweredQuestion
// framing, and the next question becomes current.
func TestAsyncQuestionsExpandAnswerAndAdvance(t *testing.T) {
	model := newAsyncQuestionModel()
	model.composer.SetValue("main draft")
	model = feedAsyncQuestions(t, model, "question-1", []any{
		map[string]any{"title": "Which database?", "options": []any{"Postgres", "SQLite"}},
		map[string]any{"title": "Deadline?"},
	})

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	if !model.asyncQuestions.Expanded() {
		t.Fatal("Alt+Up must focus the question editor")
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer = %q, want the empty first-question draft", got)
	}
	if view := model.View(); !strings.Contains(view, "1 of 2") || !strings.Contains(view, "Which database?") {
		t.Fatalf("expanded editor missing:\n%s", view)
	}

	for _, r := range "Postgres" {
		updated, _ = model.Update(keyRunes(r))
		model = updated.(*Model)
	}
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)

	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != "> Which database?\n\nPostgres" {
		t.Fatalf("submitted requests = %#v", requests)
	}
	if model.asyncQuestions.UnansweredCount() != 1 || !model.asyncQuestions.Expanded() {
		t.Fatalf("after answering: count=%d expanded=%v", model.asyncQuestions.UnansweredCount(), model.asyncQuestions.Expanded())
	}
	if question, _ := model.asyncQuestions.CurrentQuestion(); question.Title != "Deadline?" {
		t.Fatalf("current question = %#v, want Deadline?", question)
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer = %q, want the empty next draft", got)
	}
}

// TestAsyncQuestionsSkipConsumesWithoutSubmitting covers Ctrl+] skipping the
// focused question and restoring the stashed main draft once none remain.
func TestAsyncQuestionsSkipConsumesWithoutSubmitting(t *testing.T) {
	model := newAsyncQuestionModel()
	model.composer.SetValue("main draft")
	model = feedAsyncQuestions(t, model, "question-1", []any{map[string]any{"title": "Which database?"}})

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyCtrlCloseBracket})
	model = updated.(*Model)

	if model.asyncQuestions.UnansweredCount() != 0 || model.asyncQuestions.Expanded() {
		t.Fatalf("skip must consume the question: count=%d expanded=%v", model.asyncQuestions.UnansweredCount(), model.asyncQuestions.Expanded())
	}
	if len(model.SubmittedRequests()) != 0 {
		t.Fatalf("skip must not submit: %#v", model.SubmittedRequests())
	}
	if got := model.composer.Value(); got != "main draft" {
		t.Fatalf("composer = %q, want the restored main draft", got)
	}
	if view := model.View(); strings.Contains(view, "to answer") {
		t.Fatalf("summary must disappear after the last question:\n%s", view)
	}
}

// TestAsyncQuestionsEscapeCollapsesAndKeepsDraft covers `tui.question_esc_back`
// (Rust #42889): Escape returns to the composer while preserving the answer
// draft for the focused question.
func TestAsyncQuestionsEscapeCollapsesAndKeepsDraft(t *testing.T) {
	model := newAsyncQuestionModel()
	model.composer.SetValue("main draft")
	model = feedAsyncQuestions(t, model, "question-1", []any{map[string]any{"title": "Which database?"}})

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	model = updated.(*Model)
	if model.asyncQuestions.Expanded() {
		t.Fatal("Escape must collapse the question editor")
	}
	if got := model.composer.Value(); got != "main draft" {
		t.Fatalf("composer = %q, want the restored main draft", got)
	}
	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatalf("Escape must retain the question: %d", model.asyncQuestions.UnansweredCount())
	}

	// Re-focusing restores the preserved draft.
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	if model.asyncQuestions.Expanded() != true {
		t.Fatal("Alt+Up must re-focus the question")
	}
	if view := model.View(); !strings.Contains(view, "Which database?") {
		t.Fatalf("re-focused editor missing:\n%s", view)
	}
}

// TestAsyncQuestionsIgnoreReplayedMessageIDs pins Rust's replay dedup: a
// replayed completion for the same message ID cannot resurrect a handled
// question.
func TestAsyncQuestionsIgnoreReplayedMessageIDs(t *testing.T) {
	model := newAsyncQuestionModel()
	questions := []any{map[string]any{"title": "Which database?"}}
	model = feedAsyncQuestions(t, model, "question-1", questions)
	model = feedAsyncQuestions(t, model, "question-1", questions)
	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatalf("count = %d, want 1", model.asyncQuestions.UnansweredCount())
	}
}

// TestAsyncQuestionsClearedByNewPrompt covers Rust #44328: submitting a new
// prompt drops the previous prompt's questions, keeps their seen IDs so replay
// cannot restore them, and local commands leave the questions alone.
func TestAsyncQuestionsClearedByNewPrompt(t *testing.T) {
	questions := []any{map[string]any{"title": "Which database?"}}
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-1", questions)

	// A slash command preserves pending questions.
	model.composer.SetValue("/status")
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)
	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatalf("slash commands must preserve questions: %d", model.asyncQuestions.UnansweredCount())
	}

	model.composer.SetValue("a new prompt")
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)
	if model.asyncQuestions.UnansweredCount() != 0 {
		t.Fatalf("new prompt must clear questions: %d", model.asyncQuestions.UnansweredCount())
	}
	if requests := model.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != "a new prompt" {
		t.Fatalf("submitted requests = %#v", requests)
	}

	model = feedAsyncQuestions(t, model, "question-1", questions)
	if model.asyncQuestions.UnansweredCount() != 0 {
		t.Fatal("replay must not restore cleared questions")
	}
}

// TestAsyncQuestionsRejectOversizedAnswer keeps the question pending and
// surfaces Rust's limit notice.
func TestAsyncQuestionsRejectOversizedAnswer(t *testing.T) {
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-1", []any{map[string]any{"title": "Which database?"}})
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	model.composer.SetValue(strings.Repeat("x", 1<<20))

	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)
	if len(model.SubmittedRequests()) != 0 {
		t.Fatalf("oversized answer submitted: %#v", model.SubmittedRequests())
	}
	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatal("oversized answer must keep the question pending")
	}
	if !strings.Contains(model.View(), "Answer too long") {
		t.Fatalf("missing limit notice:\n%s", model.View())
	}
}

// TestAsyncQuestionsNavigationKeepsPerQuestionDrafts covers Alt+Down / Shift+
// Right navigating back toward the composer and restoring each draft.
func TestAsyncQuestionsNavigationKeepsPerQuestionDrafts(t *testing.T) {
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-1", []any{
		map[string]any{"title": "First"},
		map[string]any{"title": "Second"},
	})
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	model.composer.SetValue("first answer")

	// Alt+Up (edit_queued_message) moves forward through the questions.
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	if model.asyncQuestions.CurrentIndex() != 1 || model.composer.Value() != "" {
		t.Fatalf("forward navigation: index=%d composer=%q", model.asyncQuestions.CurrentIndex(), model.composer.Value())
	}
	model.composer.SetValue("second answer")

	// Alt+Down (prompt_stack_back) moves back and restores the first draft.
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyDown, Alt: true})
	model = updated.(*Model)
	if model.asyncQuestions.CurrentIndex() != 0 || model.composer.Value() != "first answer" {
		t.Fatalf("backward navigation: index=%d composer=%q", model.asyncQuestions.CurrentIndex(), model.composer.Value())
	}

	// Alt+Down at the first question collapses back to the main composer.
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyDown, Alt: true})
	model = updated.(*Model)
	if model.asyncQuestions.Expanded() {
		t.Fatal("backward navigation at the first question must collapse the editor")
	}
}
