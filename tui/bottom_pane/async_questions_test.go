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
