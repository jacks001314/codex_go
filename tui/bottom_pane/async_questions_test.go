package bottompane

import (
	"strings"
	"testing"

	"codex_go/turn"
)

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
	answer, limit, ready, tooLong := BuildAsyncQuestionAnswer(question, "  Postgres  ")
	if !ready || tooLong {
		t.Fatalf("ready=%v tooLong=%v", ready, tooLong)
	}
	if answer != "> Which database?\n\nPostgres" {
		t.Fatalf("answer = %q", answer)
	}
	framing := "> Which database?\n\n"
	if limit != turn.MaxUserInputTextChars-len([]rune(framing)) {
		t.Fatalf("limit = %d", limit)
	}

	if _, _, ready, tooLong := BuildAsyncQuestionAnswer(question, "   "); ready || tooLong {
		t.Fatalf("blank drafts must not submit: ready=%v tooLong=%v", ready, tooLong)
	}

	overflow := strings.Repeat("x", limit+1)
	_, gotLimit, ready, tooLong := BuildAsyncQuestionAnswer(question, overflow)
	if ready || !tooLong || gotLimit != limit {
		t.Fatalf("oversized answer = (ready=%v, tooLong=%v, limit=%d)", ready, tooLong, gotLimit)
	}

	// The framing flattens line breaks and bounds the question title at a UTF-8
	// boundary so the rendered markdown cannot be broken by the model's prompt.
	multiline := AsyncUserInputQuestion{Title: "Line one\nLine two\r\nLine three"}
	framed, _, _, _ := BuildAsyncQuestionAnswer(multiline, "answer")
	if !strings.HasPrefix(framed, "> Line one Line two  Line three\n\n") {
		t.Fatalf("framed multiline answer = %q", framed)
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
