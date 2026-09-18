package bottompane

import (
	"strings"
	"testing"
	"time"

	"codex_go/turn"
)

// TestAsyncQuestionsCollapsedCountdown covers Rust #42903: newly arrived
// collapsed questions carry a 30s countdown that only shows in its last 20
// seconds and is snoozed once the editor is opened or used.
func TestAsyncQuestionsCollapsedCountdown(t *testing.T) {
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	state := NewAsyncQuestions()
	state.AppendAt("message", []AsyncUserInputQuestion{{Title: "Which?"}}, now)

	if _, ok := state.Countdown(now); ok {
		t.Fatal("countdown must stay hidden with more than 20 seconds left")
	}
	if text, ok := state.Countdown(now.Add(12 * time.Second)); !ok || text != "18s" {
		t.Fatalf("countdown = (%q, %v), want 18s", text, ok)
	}
	if text, ok := state.Countdown(now.Add(25 * time.Second)); !ok || text != "5s" {
		t.Fatalf("countdown = (%q, %v), want 5s", text, ok)
	}
	if _, ok := state.Countdown(now.Add(31 * time.Second)); ok {
		t.Fatal("an expired countdown must disappear")
	}

	state.SnoozeAutoResolution()
	if _, ok := state.Countdown(now.Add(25 * time.Second)); ok {
		t.Fatal("snoozing must clear the countdown")
	}

	// A question that arrives while the editor is open carries no countdown.
	open := NewAsyncQuestions()
	open.AppendAt("message", []AsyncUserInputQuestion{{Title: "Which?"}}, now)
	open.SetExpanded(true, "")
	open.AppendAt("next", []AsyncUserInputQuestion{{Title: "Second?"}}, now)
	if _, ok := open.Countdown(now.Add(25 * time.Second)); ok {
		t.Fatal("questions arriving while expanded must not start a countdown")
	}
}

func TestParseAsyncUserInputQuestionsMirrorsRustWireShape(t *testing.T) {
	parsed := ParseAsyncUserInputQuestions([]any{
		map[string]any{"title": "Which database?", "options": []any{"Postgres", "SQLite"}},
		map[string]any{"title": "Deadline?"},
	})
	if len(parsed) != 2 {
		t.Fatalf("parsed = %#v, want two questions", parsed)
	}
	if parsed[0].Title != "Which database?" || len(parsed[0].Options) != 2 || parsed[0].Options[0] != "Postgres" {
		t.Fatalf("first question = %#v", parsed[0])
	}
	if parsed[1].Title != "Deadline?" || parsed[1].Options != nil {
		t.Fatalf("free-text question = %#v, want nil options", parsed[1])
	}

	for name, value := range map[string]any{
		"nil":             nil,
		"empty":           []any{},
		"missing title":   []any{map[string]any{"options": []any{"a"}}},
		"blank title":     []any{map[string]any{"title": "   "}},
		"bad option type": []any{map[string]any{"title": "q", "options": []any{42}}},
		"wrong shape":     "questions",
	} {
		if got := ParseAsyncUserInputQuestions(value); got != nil {
			t.Fatalf("%s: parsed = %#v, want nil", name, got)
		}
	}
}

func TestAsyncQuestionsAppendDeduplicatesByMessageID(t *testing.T) {
	state := NewAsyncQuestions()
	first := []AsyncUserInputQuestion{{Title: "First"}}
	second := []AsyncUserInputQuestion{{Title: "Second", Options: []string{"Named"}}}

	if !state.Append("message", first) {
		t.Fatal("first append should retain the question")
	}
	if state.Append("message", first) {
		t.Fatal("replayed message ID must not reopen questions")
	}
	if state.Append("empty", nil) {
		t.Fatal("empty question lists are ignored")
	}
	if state.UnansweredCount() != 1 || state.CurrentIndex() != 0 {
		t.Fatalf("state = %+v", state)
	}
	if !state.Append("next", second) {
		t.Fatal("second append should retain the question")
	}
	if state.UnansweredCount() != 2 || state.CurrentIndex() != 0 {
		t.Fatalf("appending must not steal focus: %+v", state)
	}
	if got := state.ProgressPrefixText(); got != "1 of 2" {
		t.Fatalf("progress = %q, want 1 of 2", got)
	}
	if !state.HasSeen("message") || state.HasSeen("unknown") {
		t.Fatal("seen IDs must be tracked")
	}
}

func TestAsyncQuestionsNavigationRestoresPerQuestionDrafts(t *testing.T) {
	state := NewAsyncQuestions()
	state.Append("message", []AsyncUserInputQuestion{{Title: "First"}, {Title: "Second"}})
	state.SetExpanded(true, "first draft")
	if !state.Expanded() {
		t.Fatal("editor should be expanded")
	}

	draft, moved := state.Navigate(true, "first draft")
	if !moved || draft != "" {
		t.Fatalf("forward navigation = (%q, %v), want empty second draft", draft, moved)
	}
	if state.CurrentIndex() != 1 {
		t.Fatalf("current index = %d, want 1", state.CurrentIndex())
	}
	if _, moved := state.Navigate(true, "second draft"); moved {
		t.Fatal("forward navigation past the last question must be a no-op")
	}
	// Rust only stores drafts when navigation succeeds; a no-op leaves the
	// stored draft untouched (the composer remains the source of truth).
	if state.CurrentDraft() != "" {
		t.Fatalf("forward no-op stored a draft: %q", state.CurrentDraft())
	}

	draft, moved = state.Navigate(false, "second draft")
	if !moved || draft != "first draft" {
		t.Fatalf("backward navigation = (%q, %v), want the first draft", draft, moved)
	}
	if _, moved := state.Navigate(false, "first draft"); moved {
		t.Fatal("backward navigation past the first question must be a no-op")
	}
	if state.CurrentDraft() != "first draft" {
		t.Fatalf("backward no-op must keep the current draft: %q", state.CurrentDraft())
	}
}

func TestAsyncQuestionsAcceptAnswerRemovesAndRefocuses(t *testing.T) {
	state := NewAsyncQuestions()
	state.Append("message", []AsyncUserInputQuestion{{Title: "First"}, {Title: "Second"}, {Title: "Third"}})
	state.SetExpanded(true, "")

	// Skip "First"; the following question becomes current.
	if draft := state.AcceptAnswer(); draft != "" {
		t.Fatalf("accepted draft = %q, want empty", draft)
	}
	if question, ok := state.CurrentQuestion(); !ok || question.Title != "Second" {
		t.Fatalf("current question = %#v (ok=%v), want Second", question, ok)
	}
	if state.UnansweredCount() != 2 || !state.Expanded() {
		t.Fatalf("state = %+v", state)
	}

	// Removing the last visible entry wraps back to the first remaining one.
	state.Navigate(true, "")
	state.AcceptAnswer()
	if question, ok := state.CurrentQuestion(); !ok || question.Title != "Second" {
		t.Fatalf("current question = %#v (ok=%v), want Second", question, ok)
	}
	state.AcceptAnswer()
	if state.UnansweredCount() != 0 || state.Expanded() {
		t.Fatalf("state = %+v, want empty and collapsed", state)
	}
	if state.AcceptAnswer() != "" {
		t.Fatal("accepting with no pending questions is a no-op")
	}
}

func TestAsyncQuestionsSetExpandedRefusesEmptyState(t *testing.T) {
	state := NewAsyncQuestions()
	state.SetExpanded(true, "ignored")
	if state.Expanded() {
		t.Fatal("empty state must not expand")
	}
	state.Append("message", []AsyncUserInputQuestion{{Title: "First"}})
	state.SetExpanded(true, "draft")
	if !state.Expanded() || state.CurrentDraft() != "draft" {
		t.Fatalf("state = %+v", state)
	}
	state.SetExpanded(false, "later draft")
	if state.Expanded() || state.CurrentDraft() != "later draft" {
		t.Fatalf("collapse must store the draft: %+v", state)
	}
}

func TestBuildAsyncQuestionAnswerFramesAndBoundsTheAnswer(t *testing.T) {
	question := AsyncUserInputQuestion{Title: "Which database?"}
	reply, ready, tooLong := BuildAsyncQuestionAnswer("question-id", question, "  Postgres  ")
	if !ready || tooLong {
		t.Fatalf("ready=%v tooLong=%v", ready, tooLong)
	}
	if !strings.HasPrefix(reply, "<send_user_message_question_reply>\n") ||
		!strings.HasSuffix(reply, "\n</send_user_message_question_reply>") {
		t.Fatalf("reply = %q", reply)
	}
	if !strings.Contains(reply, `"answer":"Postgres"`) ||
		!strings.Contains(reply, `"question":"Which database?"`) ||
		!strings.Contains(reply, `"questionItemId":"question-id"`) {
		t.Fatalf("reply envelope = %q", reply)
	}

	if _, ready, tooLong := BuildAsyncQuestionAnswer("question-id", question, "   "); ready || tooLong {
		t.Fatalf("blank drafts must not submit: ready=%v tooLong=%v", ready, tooLong)
	}

	// The limit is enforced on the rendered reply, so JSON escaping counts.
	overflow := strings.Repeat("x", turn.MaxUserInputTextChars)
	if _, ready, tooLong := BuildAsyncQuestionAnswer("question-id", question, overflow); ready || !tooLong {
		t.Fatalf("oversized answer = (ready=%v, tooLong=%v)", ready, tooLong)
	}

	// The question is flattened and bounded so the envelope cannot be broken by
	// the model's prompt.
	multiline := AsyncUserInputQuestion{Title: "Line one\nLine two\r\nLine three"}
	framed, _, _ := BuildAsyncQuestionAnswer("id", multiline, "answer")
	if !strings.Contains(framed, `"question":"Line one Line two  Line three"`) {
		t.Fatalf("framed multiline answer = %q", framed)
	}

	// An oversized identity keeps the plain-text framing instead of an envelope.
	plain, _, _ := BuildAsyncQuestionAnswer(strings.Repeat("i", 513), question, "answer")
	if plain != "> Which database?\n\nanswer" {
		t.Fatalf("oversized identity reply = %q", plain)
	}
}

// TestAsyncQuestionsResolveAnswersLikeRust mirrors Rust #46486's
// answered_questions_do_not_reopen_when_history_precedes_local_drafts: a
// committed reply resolves its question by identity, an older reply naming only
// the source message resolves that whole message, and an unknown id changes
// nothing.
func TestAsyncQuestionsResolveAnswersLikeRust(t *testing.T) {
	untouched := NewAsyncQuestions()
	untouched.AppendAt("message", []AsyncUserInputQuestion{{Title: "First"}, {Title: "Second"}}, time.Now())
	if resolved, _ := untouched.ResolveAsyncQuestionAnswers([]string{"unknown"}); resolved {
		t.Fatal("an unknown identity resolved a question")
	}
	if untouched.UnansweredCount() != 2 {
		t.Fatalf("pending = %d, want 2", untouched.UnansweredCount())
	}

	// History can precede the live questions: the identity is remembered, so a
	// later arrival of the same question does not reopen it.
	state := NewAsyncQuestions()
	state.ResolveAsyncQuestionAnswers([]string{asyncQuestionIdentity("message", 0)})
	state.AppendAt("message", []AsyncUserInputQuestion{{Title: "First"}, {Title: "Second"}}, time.Now())
	if state.UnansweredCount() != 1 {
		t.Fatalf("pending = %d, want only the unanswered question", state.UnansweredCount())
	}
	if question, ok := state.CurrentQuestion(); !ok || question.Title != "Second" {
		t.Fatalf("focused question = %#v, %v", question, ok)
	}
	// An unrelated identity leaves the surviving question and its focus intact.
	if resolved, _ := state.ResolveAsyncQuestionAnswers([]string{asyncQuestionIdentity("other", 0)}); resolved {
		t.Fatal("an unrelated identity resolved the surviving question")
	}
	if state.UnansweredCount() != 1 || state.CurrentIndex() != 0 {
		t.Fatalf("unrelated resolution changed the state: %+v", state)
	}
	// The legacy reply names only the source message, which resolves the rest.
	if resolved, currentAnswered := state.ResolveAsyncQuestionAnswers([]string{"message"}); !resolved || !currentAnswered {
		t.Fatal("the legacy message-scoped reply did not resolve")
	}
	if state.UnansweredCount() != 0 || state.Expanded() {
		t.Fatalf("state after resolving = %+v", state)
	}
}

// TestOversizedQuestionIDsKeepQuestionsAnswerableLikeRust mirrors Rust's
// oversized_question_ids_keep_questions_answerable_without_echoing_the_id: an
// over-long identity still submits the plain-text framing.
func TestOversizedQuestionIDsKeepQuestionsAnswerableLikeRust(t *testing.T) {
	state := NewAsyncQuestions()
	state.AppendAt(strings.Repeat("x", 1024), []AsyncUserInputQuestion{{Title: "Question"}}, time.Now())
	if state.UnansweredCount() != 1 {
		t.Fatalf("pending = %d, want 1", state.UnansweredCount())
	}
	question, ok := state.CurrentQuestion()
	if !ok {
		t.Fatal("question was not retained")
	}
	reply, ready, tooLong := BuildAsyncQuestionAnswer(state.CurrentQuestionID(), question, "Answer")
	if !ready || tooLong {
		t.Fatalf("ready=%v tooLong=%v", ready, tooLong)
	}
	if reply != "> Question\n\nAnswer" {
		t.Fatalf("reply = %q, want the plain-text framing without the id", reply)
	}
}

// TestAsyncQuestionsBoundSuggestedOptions covers Rust #42894: at most 32
// suggestions, labels longer than 512 bytes dropped, and a question whose
// suggestions all drop behaves as free text.
func TestAsyncQuestionsBoundSuggestedOptions(t *testing.T) {
	state := NewAsyncQuestions()
	options := make([]string, 0, 40)
	for index := 0; index < 40; index++ {
		options = append(options, "option")
	}
	options = append(options, strings.Repeat("x", asyncQuestionMaxOptionBytes+1))
	state.Append("message", []AsyncUserInputQuestion{{Title: "Bounded", Options: options}})
	question, _ := state.CurrentQuestion()
	if len(question.Options) != asyncQuestionMaxOptions {
		t.Fatalf("bounds option count = %d, want %d", len(question.Options), asyncQuestionMaxOptions)
	}
	for _, label := range question.Options {
		if len(label) > asyncQuestionMaxOptionBytes {
			t.Fatalf("oversized suggestion retained: %d bytes", len(label))
		}
	}
	if !state.HasOptions() || state.ChoiceCount() != asyncQuestionMaxOptions+1 {
		t.Fatalf("choice count = %d (hasOptions=%v)", state.ChoiceCount(), state.HasOptions())
	}

	allOversized := NewAsyncQuestions()
	allOversized.Append("next", []AsyncUserInputQuestion{{Title: "Free", Options: []string{strings.Repeat("y", asyncQuestionMaxOptionBytes+1)}}})
	if allOversized.HasOptions() || !allOversized.FocusIsNotes() {
		t.Fatal("a question whose suggestions all drop must behave as free text")
	}
}

// TestAsyncQuestionsChoiceSelectionAndAnswer covers Rust #42894/#42897's choice
// model: a default selection, wrapping navigation with the appended Other row,
// and answer text taken from the focused choice or the editable draft.
func TestAsyncQuestionsChoiceSelectionAndAnswer(t *testing.T) {
	state := NewAsyncQuestions()
	state.Append("message", []AsyncUserInputQuestion{{Title: "Which?", Options: []string{"Postgres", "SQLite"}}})
	state.SetExpanded(true, "")
	if state.SelectedOptionIndex() != 0 || state.OtherSelected() || state.FocusIsNotes() {
		t.Fatalf("default selection = %d (other=%v notes=%v)", state.SelectedOptionIndex(), state.OtherSelected(), state.FocusIsNotes())
	}
	if state.ChoiceCount() != 3 {
		t.Fatalf("choice count = %d, want 3 (two suggestions plus Other)", state.ChoiceCount())
	}
	if text, ready := state.AnswerText(""); !ready || text != "Postgres" {
		t.Fatalf("default answer = (%q, %v)", text, ready)
	}
	if !state.AnswerIsNamedChoice() {
		t.Fatal("the default selection is a named choice")
	}

	state.MoveSelection(true)
	if state.SelectedOptionIndex() != 1 {
		t.Fatalf("move down selection = %d", state.SelectedOptionIndex())
	}
	if text, _ := state.AnswerText(""); text != "SQLite" {
		t.Fatalf("second choice answer = %q", text)
	}
	state.MoveSelection(true)
	if !state.OtherSelected() || !state.FocusIsNotes() || state.AnswerIsNamedChoice() {
		t.Fatalf("third row must focus Other: %+v", state)
	}
	if _, ready := state.AnswerText("   "); ready {
		t.Fatal("a blank Other answer must not be ready")
	}
	if text, ready := state.AnswerText("  custom answer  "); !ready || text != "custom answer" {
		t.Fatalf("Other answer = (%q, %v)", text, ready)
	}

	// Wrapping continues through Other back to the first suggestion.
	state.MoveSelection(true)
	if state.SelectedOptionIndex() != 0 {
		t.Fatalf("wrapped selection = %d", state.SelectedOptionIndex())
	}
	state.MoveSelection(false)
	if !state.OtherSelected() {
		t.Fatalf("backward wrap selection = %d", state.SelectedOptionIndex())
	}
	state.JumpSelection(true)
	if state.SelectedOptionIndex() != 0 {
		t.Fatalf("jump top = %d", state.SelectedOptionIndex())
	}
	state.JumpSelection(false)
	if !state.OtherSelected() {
		t.Fatalf("jump bottom = %d", state.SelectedOptionIndex())
	}
	state.PageSelection(false)
	if state.SelectedOptionIndex() != 0 {
		t.Fatalf("page up = %d", state.SelectedOptionIndex())
	}
	state.PageSelection(true)
	if !state.OtherSelected() {
		t.Fatalf("page down = %d", state.SelectedOptionIndex())
	}
}

// TestAsyncQuestionsOtherPlaceholderDistinguishesSuggestedOther covers Rust
// #42897: a suggested option literally named Other gets a distinct placeholder.
func TestAsyncQuestionsOtherPlaceholderDistinguishesSuggestedOther(t *testing.T) {
	plain := NewAsyncQuestions()
	plain.Append("message", []AsyncUserInputQuestion{{Title: "Q", Options: []string{"A"}}})
	if got := plain.OtherPlaceholder(); got != otherOptionLabel {
		t.Fatalf("placeholder = %q, want %q", got, otherOptionLabel)
	}
	if got := plain.OtherLabel(); got != otherOptionLabel {
		t.Fatalf("Other row label = %q, want the placeholder", got)
	}

	named := NewAsyncQuestions()
	named.Append("message", []AsyncUserInputQuestion{{Title: "Q", Options: []string{"other", "A"}}})
	if got := named.OtherPlaceholder(); got != "Other (write an answer)" {
		t.Fatalf("named-Other placeholder = %q", got)
	}
	named.SelectOther()
	if got := named.OtherPlaceholder(); got != "Other (write an answer)" {
		t.Fatalf("selected placeholder = %q", got)
	}

	// A non-focused Other row previews the custom draft, bounded like Rust.
	drafting := NewAsyncQuestions()
	drafting.Append("message", []AsyncUserInputQuestion{{Title: "Q", Options: []string{"A"}}})
	drafting.Append("next", []AsyncUserInputQuestion{{Title: "Q2"}})
	drafting.SetExpanded(true, "")
	drafting.Navigate(true, strings.Repeat("d", 600))
	drafting.Navigate(false, "")
	preview := drafting.OtherLabel()
	if len(preview) > 128 || strings.ContainsAny(preview, "\n") {
		t.Fatalf("Other preview = %q (%d runes)", preview, len([]rune(preview)))
	}
	if preview == otherOptionLabel {
		t.Fatal("a non-empty draft must preview instead of the placeholder")
	}
}
