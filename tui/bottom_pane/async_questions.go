package bottompane

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"codex_go/context"
	"codex_go/turn"
)

// Rust parity: codex-rs/tui/src/bottom_pane/async_questions/{mod,state}.rs and
// codex-rs/protocol/src/items.rs (AsyncUserInputQuestion).
//
// Async questions arrive as agent-message items while a turn is still running.
// They are locally retained as drafts: only an accepted local submission
// removes a question, and handled message IDs stay recorded so replay cannot
// reopen an answered or skipped question.

// AsyncUserInputQuestion mirrors Rust's codex_protocol::items::AsyncUserInputQuestion.
// A nil Options slice is a free-text-only question; Rust serializes it as null.
type AsyncUserInputQuestion struct {
	Title   string
	Options []string
}

// PendingAsyncQuestion retains one unanswered question and its draft answer.
type PendingAsyncQuestion struct {
	Question AsyncUserInputQuestion
	Draft    string
}

// AsyncQuestions is the locally retained async-question editor state.
type AsyncQuestions struct {
	pending  []PendingAsyncQuestion
	current  int
	seen     map[string]struct{}
	expanded bool
}

// NewAsyncQuestions returns empty async-question state.
func NewAsyncQuestions() *AsyncQuestions {
	return &AsyncQuestions{seen: map[string]struct{}{}}
}

// ParseAsyncUserInputQuestions decodes the structured question payload carried
// by an async agent message (Rust #42178: title plus optional options). It
// accepts the app-server's []any/map shape and Go-native values, and ignores
// entries without a usable title.
func ParseAsyncUserInputQuestions(value any) []AsyncUserInputQuestion {
	switch typed := value.(type) {
	case nil:
		return nil
	case []AsyncUserInputQuestion:
		return append([]AsyncUserInputQuestion(nil), typed...)
	case []any:
		out := make([]AsyncUserInputQuestion, 0, len(typed))
		for _, entry := range typed {
			if question, ok := asyncUserInputQuestionFromValue(entry); ok {
				out = append(out, question)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case []map[string]any:
		out := make([]AsyncUserInputQuestion, 0, len(typed))
		for _, entry := range typed {
			if question, ok := asyncUserInputQuestionFromValue(entry); ok {
				out = append(out, question)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}

func asyncUserInputQuestionFromValue(value any) (AsyncUserInputQuestion, bool) {
	entry, ok := value.(map[string]any)
	if !ok {
		return AsyncUserInputQuestion{}, false
	}
	title, _ := entry["title"].(string)
	if strings.TrimSpace(title) == "" {
		return AsyncUserInputQuestion{}, false
	}
	question := AsyncUserInputQuestion{Title: title}
	switch options := entry["options"].(type) {
	case nil:
	case []any:
		parsed := make([]string, 0, len(options))
		for _, option := range options {
			text, ok := option.(string)
			if !ok {
				return AsyncUserInputQuestion{}, false
			}
			parsed = append(parsed, text)
		}
		question.Options = parsed
	case []string:
		question.Options = append([]string(nil), options...)
	default:
		return AsyncUserInputQuestion{}, false
	}
	return question, true
}

// Append retains newly arrived questions, mirroring Rust's
// AsyncQuestions::append. An already-seen message ID or an empty list is a
// no-op. It reports whether the pending set grew.
func (q *AsyncQuestions) Append(messageID string, questions []AsyncUserInputQuestion) bool {
	if q == nil || len(questions) == 0 {
		return false
	}
	if q.seen == nil {
		q.seen = map[string]struct{}{}
	}
	messageID = strings.TrimSpace(messageID)
	if messageID != "" {
		if _, ok := q.seen[messageID]; ok {
			return false
		}
		q.seen[messageID] = struct{}{}
	}
	wasEmpty := len(q.pending) == 0
	for _, question := range questions {
		q.pending = append(q.pending, PendingAsyncQuestion{Question: question})
	}
	if wasEmpty {
		q.current = 0
	}
	return true
}

// HasSeen reports whether a message ID was already consumed by Append.
func (q *AsyncQuestions) HasSeen(messageID string) bool {
	if q == nil {
		return false
	}
	_, ok := q.seen[strings.TrimSpace(messageID)]
	return ok
}

// UnansweredCount returns the number of retained questions.
func (q *AsyncQuestions) UnansweredCount() int {
	if q == nil {
		return 0
	}
	return len(q.pending)
}

// CurrentIndex returns the zero-based index of the focused question.
func (q *AsyncQuestions) CurrentIndex() int {
	if q == nil {
		return 0
	}
	return q.current
}

// CurrentQuestion returns the focused question.
func (q *AsyncQuestions) CurrentQuestion() (AsyncUserInputQuestion, bool) {
	if q == nil || q.current < 0 || q.current >= len(q.pending) {
		return AsyncUserInputQuestion{}, false
	}
	return q.pending[q.current].Question, true
}

// ProgressPrefixText mirrors Rust's "N of total" progress label.
func (q *AsyncQuestions) ProgressPrefixText() string {
	if q == nil || len(q.pending) == 0 {
		return ""
	}
	return strconv.Itoa(q.current+1) + " of " + strconv.Itoa(len(q.pending))
}

// Expanded reports whether the question editor owns the composer.
func (q *AsyncQuestions) Expanded() bool {
	return q != nil && q.expanded
}

// SetExpanded focuses the question editor, storing the composer draft for the
// current question first. Expansion is refused when no questions remain.
func (q *AsyncQuestions) SetExpanded(expanded bool, draft string) {
	if q == nil {
		return
	}
	q.storeCurrentDraft(draft)
	q.expanded = expanded && len(q.pending) > 0
}

// Navigate moves the focused question, returning the draft to restore and
// whether the focus changed. Backward navigation past the first question and
// forward navigation past the last question are no-ops.
func (q *AsyncQuestions) Navigate(forward bool, draft string) (string, bool) {
	if q == nil {
		return draft, false
	}
	next := q.current + 1
	if !forward {
		next = q.current - 1
	}
	if next < 0 || next >= len(q.pending) {
		return draft, false
	}
	q.storeCurrentDraft(draft)
	q.current = next
	return q.currentDraft(), true
}

// CurrentDraft returns the stored draft of the focused question.
func (q *AsyncQuestions) CurrentDraft() string {
	if q == nil {
		return ""
	}
	return q.currentDraft()
}

// ClearPending drops every retained question without forgetting handled message
// IDs (Rust #44328): moving on to a new prompt clears the prompt's questions,
// and replay cannot restore them.
func (q *AsyncQuestions) ClearPending() {
	if q == nil {
		return
	}
	q.pending = nil
	q.current = 0
	q.expanded = false
}

// AcceptAnswer removes the focused question, mirroring Rust's
// AsyncQuestions::accept_answer, and returns the draft of the newly focused
// question (empty when none remain).
func (q *AsyncQuestions) AcceptAnswer() string {
	if q == nil || len(q.pending) == 0 {
		return ""
	}
	q.pending = append(q.pending[:q.current], q.pending[q.current+1:]...)
	if q.current >= len(q.pending) {
		q.current = 0
	}
	if len(q.pending) == 0 {
		q.expanded = false
	}
	return q.currentDraft()
}

func (q *AsyncQuestions) storeCurrentDraft(draft string) {
	if q.current < 0 || q.current >= len(q.pending) {
		return
	}
	q.pending[q.current].Draft = draft
}

func (q *AsyncQuestions) currentDraft() string {
	if q.current < 0 || q.current >= len(q.pending) {
		return ""
	}
	return q.pending[q.current].Draft
}

// BuildAsyncQuestionAnswer mirrors Rust's
// AsyncQuestions::go_next_or_submit: trim the draft, prepend the bounded
// AnsweredQuestion framing, and enforce the user-input character limit on the
// whole message. It returns ready=false when the answer is empty, and
// tooLong=true (with the remaining limit for the footer flash) when it does not
// fit.
func BuildAsyncQuestionAnswer(question AsyncUserInputQuestion, draft string) (answer string, limit int, ready bool, tooLong bool) {
	framing := context.NewAnsweredQuestion(question.Title).Body()
	limit = turn.MaxUserInputTextChars - utf8.RuneCountInString(framing)
	text := strings.TrimSpace(draft)
	if utf8.RuneCountInString(text) > limit {
		return "", limit, false, true
	}
	if text == "" {
		return "", limit, false, false
	}
	return framing + text, limit, true, false
}
