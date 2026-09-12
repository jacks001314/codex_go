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

func withChoiceQuestion(t *testing.T) *Model {
	t.Helper()
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-1", []any{
		map[string]any{"title": "Which database?", "options": []any{"Postgres", "SQLite"}},
	})
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	return updated.(*Model)
}

// TestAsyncQuestionsRenderAndSubmitSelectedChoice covers Rust #42894: suggested
// answers render as numbered choices with a default selection, and Enter
// submits the selected label.
func TestAsyncQuestionsRenderAndSubmitSelectedChoice(t *testing.T) {
	model := withChoiceQuestion(t)
	view := model.View()
	for _, want := range []string{"› 1. Postgres", "  2. SQLite", "  3. Other", "Enter submit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("choice view missing %q:\n%s", want, view)
		}
	}

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != "> Which database?\n\nPostgres" {
		t.Fatalf("submitted requests = %#v", requests)
	}
	if model.asyncQuestions.UnansweredCount() != 0 {
		t.Fatal("answering must consume the question")
	}
}

// TestAsyncQuestionsArrowNavigationSelectsNextChoice covers list navigation
// moving the focused choice.
func TestAsyncQuestionsArrowNavigationSelectsNextChoice(t *testing.T) {
	model := withChoiceQuestion(t)
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyDown})
	model = updated.(*Model)
	if model.asyncQuestions.SelectedOptionIndex() != 1 {
		t.Fatalf("selection = %d, want 1", model.asyncQuestions.SelectedOptionIndex())
	}
	if view := model.View(); !strings.Contains(view, "› 2. SQLite") {
		t.Fatalf("selection marker missing:\n%s", view)
	}
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)
	if requests := model.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != "> Which database?\n\nSQLite" {
		t.Fatalf("submitted requests = %#v", requests)
	}
}

// TestAsyncQuestionsDigitShortcutSubmitsNamedChoice covers Rust #42894's digit
// shortcuts submitting a named choice directly.
func TestAsyncQuestionsDigitShortcutSubmitsNamedChoice(t *testing.T) {
	model := withChoiceQuestion(t)
	updated, _ := model.Update(keyRunes('2'))
	model = updated.(*Model)
	if requests := model.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != "> Which database?\n\nSQLite" {
		t.Fatalf("submitted requests = %#v", requests)
	}
	if model.asyncQuestions.UnansweredCount() != 0 {
		t.Fatal("digit submission must consume the question")
	}
}

// TestAsyncQuestionsTypingOpensOther covers Rust #42897: typing (including the
// printable default list keys) opens the editable Other choice instead of
// navigating, and the custom draft is submitted as the answer.
func TestAsyncQuestionsTypingOpensOther(t *testing.T) {
	model := withChoiceQuestion(t)
	updated, _ := model.Update(keyRunes('k'))
	model = updated.(*Model)
	if model.composer.Value() != "k" {
		t.Fatalf("composer = %q, want the typed Other draft", model.composer.Value())
	}
	if !model.asyncQuestions.OtherSelected() {
		t.Fatalf("selection = %d, want the Other row", model.asyncQuestions.SelectedOptionIndex())
	}
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)
	if requests := model.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != "> Which database?\n\nk" {
		t.Fatalf("submitted requests = %#v", requests)
	}
}

// TestAsyncQuestionsBlankOtherIsNotSubmitted pins Rust #42897's blank-Other
// rejection.
func TestAsyncQuestionsBlankOtherIsNotSubmitted(t *testing.T) {
	model := withChoiceQuestion(t)
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyDown})
	model = updated.(*Model)
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyDown})
	model = updated.(*Model)
	if !model.asyncQuestions.OtherSelected() {
		t.Fatal("two moves should focus the Other row")
	}
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)
	if len(model.SubmittedRequests()) != 0 {
		t.Fatalf("blank Other submitted: %#v", model.SubmittedRequests())
	}
	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatal("blank Other must keep the question pending")
	}
}

// TestAsyncQuestionsBlockClippedChoice mirrors Rust #42894's visibility guard: a
// suggested option the terminal cannot show in full must not be submittable.
func TestAsyncQuestionsBlockClippedChoice(t *testing.T) {
	options := []any{"Postgres", "SQLite", "MySQL", "Oracle", "SQL Server"}
	feed := func(height int) *Model {
		model := NewModel(codextui.NewState(nil), Options{Width: 60, Height: height})
		model.State.SetThreadID("thread-questions")
		model = feedAsyncQuestions(t, model, "question-1", []any{
			map[string]any{"title": "Which database should we deploy to production?", "options": options},
		})
		updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
		return updated.(*Model)
	}

	clipped := feed(8)
	updated, _ := clipped.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	clipped = updated.(*Model)
	if len(clipped.SubmittedRequests()) != 0 {
		t.Fatalf("clipped choice was submitted: %#v", clipped.SubmittedRequests())
	}
	if !strings.Contains(clipped.View(), "Expand terminal to read the entire option") {
		t.Fatalf("missing clipped-choice notice:\n%s", clipped.View())
	}
	if clipped.asyncQuestions.UnansweredCount() != 1 {
		t.Fatal("clipped choice must keep the question pending")
	}

	roomy := feed(60)
	updated, _ = roomy.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	roomy = updated.(*Model)
	if requests := roomy.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != "> Which database should we deploy to production?\n\nPostgres" {
		t.Fatalf("roomy submitted requests = %#v", requests)
	}
}
