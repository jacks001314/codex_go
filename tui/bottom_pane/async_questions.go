package bottompane

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
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

const (
	// asyncQuestionMaxOptions and asyncQuestionMaxOptionBytes bound the
	// model-authored suggestions before they are cloned or rendered
	// (Rust #42894).
	asyncQuestionMaxOptions     = 32
	asyncQuestionMaxOptionBytes = 512
	// otherOptionLabel is the editable free-text choice appended to a question
	// with suggestions (Rust #42897).
	otherOptionLabel = "Other"
	// asyncQuestionAutoResolveWindow is how long a newly arrived collapsed
	// question shows its countdown (Rust #42903).
	asyncQuestionAutoResolveWindow = 30 * time.Second
	// asyncQuestionCountdownVisibleAfter bounds when the countdown appears.
	asyncQuestionCountdownVisibleAfter = 20 * time.Second
)

// PendingAsyncQuestion retains one unanswered question, its choice selection,
// and its draft answer. SelectedOption indexes the suggested options, with
// len(Options) selecting the appended Other choice.
type PendingAsyncQuestion struct {
	MessageID string
	// QuestionID is the desktop's stable per-question identity, the JSON
	// encoding of ["request_user_input_async", message id, question index]
	// (Rust #46486).
	QuestionID     string
	Question       AsyncUserInputQuestion
	SelectedOption int
	Draft          string
	// ExpiresAt drives the collapsed countdown; the zero value means the
	// question has been snoozed by opening or using the editor.
	ExpiresAt time.Time
}

// AsyncQuestions is the locally retained async-question editor state.
type AsyncQuestions struct {
	pending []PendingAsyncQuestion
	current int
	seen    map[string]struct{}
	// answered retains question identities answered on another client so
	// replay (or a late live event) cannot reopen them (Rust #46486).
	answered map[string]struct{}
	expanded bool
}

// NewAsyncQuestions returns empty async-question state.
func NewAsyncQuestions() *AsyncQuestions {
	return &AsyncQuestions{seen: map[string]struct{}{}, answered: map[string]struct{}{}}
}

// asyncQuestionIdentity mirrors the desktop's JSON.stringify([tool name, item
// id, question index]) identity (Rust #46486).
func asyncQuestionIdentity(messageID string, index int) string {
	encoded, err := json.Marshal([]any{"request_user_input_async", messageID, index})
	if err != nil {
		return ""
	}
	return string(encoded)
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
	return q.AppendAt(messageID, questions, time.Now())
}

// AppendAt is Append with an explicit arrival time so the collapsed countdown
// and its tests are deterministic (Rust #42903).
func (q *AsyncQuestions) AppendAt(messageID string, questions []AsyncUserInputQuestion, now time.Time) bool {
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
	var expiresAt time.Time
	if !q.expanded {
		expiresAt = now.Add(asyncQuestionAutoResolveWindow)
	}
	grew := false
	for index, question := range questions {
		// A question already answered on another client (by identity, or by an
		// older reply naming only its source message) must not reopen.
		questionID := asyncQuestionIdentity(messageID, index)
		if _, ok := q.answered[questionID]; ok {
			continue
		}
		if messageID != "" {
			if _, ok := q.answered[messageID]; ok {
				continue
			}
		}
		q.pending = append(q.pending, PendingAsyncQuestion{
			MessageID:  messageID,
			QuestionID: questionID,
			Question:   filterAsyncUserInputQuestionOptions(question),
			ExpiresAt:  expiresAt,
		})
		grew = true
	}
	if wasEmpty {
		q.current = 0
	}
	return grew
}

// ResolveAsyncQuestionAnswers mirrors Rust's AsyncQuestions::resolve_answers
// (#46486): committed desktop replies resolve the questions they name, an older
// reply that names only its source message resolves that whole message, and the
// answered identities stay recorded so replay cannot reopen them. It reports
// whether any pending question was resolved, and whether the focused question
// was among them (the caller then re-syncs the composer).
func (q *AsyncQuestions) ResolveAsyncQuestionAnswers(questionIDs []string) (bool, bool) {
	if q == nil || len(questionIDs) == 0 {
		return false, false
	}
	if q.answered == nil {
		q.answered = map[string]struct{}{}
	}
	resolved := make(map[string]struct{}, len(questionIDs))
	for _, id := range questionIDs {
		if id = strings.TrimSpace(id); id == "" {
			continue
		}
		q.answered[id] = struct{}{}
		resolved[id] = struct{}{}
	}
	if len(resolved) == 0 {
		return false, false
	}
	isAnswered := func(question PendingAsyncQuestion) bool {
		if _, ok := resolved[question.QuestionID]; ok {
			return true
		}
		if question.MessageID == "" {
			return false
		}
		_, ok := resolved[question.MessageID]
		return ok
	}
	hadPending := false
	for _, question := range q.pending {
		if isAnswered(question) {
			hadPending = true
			break
		}
	}
	if !hadPending {
		return false, false
	}
	currentAnswered := q.current >= 0 && q.current < len(q.pending) && isAnswered(q.pending[q.current])
	// Keep the cursor on the nearest preceding unanswered question.
	current := 0
	for index := 0; index < q.current && index < len(q.pending); index++ {
		if !isAnswered(q.pending[index]) {
			current++
		}
	}
	kept := make([]PendingAsyncQuestion, 0, len(q.pending))
	for _, question := range q.pending {
		if !isAnswered(question) {
			kept = append(kept, question)
		}
	}
	q.pending = kept
	if current < len(q.pending) {
		q.current = current
	} else {
		q.current = 0
	}
	if len(q.pending) == 0 {
		q.expanded = false
	}
	return true, currentAnswered
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

// CurrentQuestionID returns the focused question's stable desktop identity, or
// an empty string when no question is focused or the identity cannot be
// encoded.
func (q *AsyncQuestions) CurrentQuestionID() string {
	if q == nil || q.current < 0 || q.current >= len(q.pending) {
		return ""
	}
	return q.pending[q.current].QuestionID
}

// filterAsyncUserInputQuestionOptions bounds model-authored suggestions before
// they are retained or rendered: at most 32 labels, each at most 512 bytes
// (Rust #42894). Filtering everything out leaves a free-text question.
func filterAsyncUserInputQuestionOptions(question AsyncUserInputQuestion) AsyncUserInputQuestion {
	if question.Options == nil {
		return question
	}
	filtered := make([]string, 0, len(question.Options))
	for _, label := range question.Options {
		if len(filtered) >= asyncQuestionMaxOptions {
			break
		}
		if len(label) > asyncQuestionMaxOptionBytes {
			continue
		}
		filtered = append(filtered, label)
	}
	question.Options = filtered
	return question
}

// HasOptions reports whether the focused question has usable suggestions.
func (q *AsyncQuestions) HasOptions() bool {
	if q == nil || q.current < 0 || q.current >= len(q.pending) {
		return false
	}
	return len(q.pending[q.current].Question.Options) > 0
}

// NamedOptionCount returns the number of suggested options on the focused
// question.
func (q *AsyncQuestions) NamedOptionCount() int {
	if q == nil || q.current < 0 || q.current >= len(q.pending) {
		return 0
	}
	return len(q.pending[q.current].Question.Options)
}

// ChoiceCount returns the number of selectable rows: every suggested option
// plus the appended editable Other choice (Rust #42897).
func (q *AsyncQuestions) ChoiceCount() int {
	if !q.HasOptions() {
		return 0
	}
	return q.NamedOptionCount() + 1
}

// OtherSelected reports whether the editable Other choice is focused.
func (q *AsyncQuestions) OtherSelected() bool {
	return q != nil && q.HasOptions() && q.pending[q.current].SelectedOption == q.NamedOptionCount()
}

// FocusIsNotes reports whether the composer edits the answer text: either the
// question has no suggestions or the editable Other choice is focused.
func (q *AsyncQuestions) FocusIsNotes() bool {
	return q != nil && (!q.HasOptions() || q.OtherSelected())
}

// SelectedOptionIndex returns the focused choice index.
func (q *AsyncQuestions) SelectedOptionIndex() int {
	if q == nil || q.current < 0 || q.current >= len(q.pending) {
		return 0
	}
	return q.pending[q.current].SelectedOption
}

// SelectOption focuses a choice row, including the appended Other row.
func (q *AsyncQuestions) SelectOption(index int) {
	if q == nil || q.current < 0 || q.current >= len(q.pending) {
		return
	}
	if index < 0 || index >= q.ChoiceCount() {
		return
	}
	q.pending[q.current].SelectedOption = index
}

// SelectOther focuses the editable Other choice.
func (q *AsyncQuestions) SelectOther() {
	if q == nil || !q.HasOptions() {
		return
	}
	q.SelectOption(q.NamedOptionCount())
}

// MoveSelection moves the focused choice by one row, wrapping at the ends.
func (q *AsyncQuestions) MoveSelection(forward bool) bool {
	count := q.ChoiceCount()
	if count == 0 {
		return false
	}
	next := q.SelectedOptionIndex() + 1
	if !forward {
		next = q.SelectedOptionIndex() - 1
	}
	if next < 0 {
		next = count - 1
	}
	if next >= count {
		next = 0
	}
	q.SelectOption(next)
	return true
}

// PageSelection moves the focused choice by one page, clamped to the list. Go
// renders the whole option list, so a page spans every row and the move lands
// on the first or last choice (Rust clamps by the visible row count).
func (q *AsyncQuestions) PageSelection(forward bool) bool {
	count := q.ChoiceCount()
	if count == 0 {
		return false
	}
	if forward {
		q.SelectOption(count - 1)
	} else {
		q.SelectOption(0)
	}
	return true
}

// JumpSelection moves the focused choice to the first or last row.
func (q *AsyncQuestions) JumpSelection(top bool) bool {
	count := q.ChoiceCount()
	if count == 0 {
		return false
	}
	if top {
		q.SelectOption(0)
	} else {
		q.SelectOption(count - 1)
	}
	return true
}

// OtherPlaceholder is the editable choice's placeholder. A suggested option
// literally named Other forces a distinct placeholder (Rust #42897).
func (q *AsyncQuestions) OtherPlaceholder() string {
	if question, ok := q.CurrentQuestion(); ok {
		for _, label := range question.Options {
			if strings.EqualFold(label, otherOptionLabel) {
				return "Other (write an answer)"
			}
		}
	}
	return otherOptionLabel
}

// OtherLabel is the text shown on the Other choice row: the placeholder while
// the row is focused, otherwise a bounded preview of the custom draft.
func (q *AsyncQuestions) OtherLabel() string {
	placeholder := q.OtherPlaceholder()
	if q == nil || q.OtherSelected() {
		return placeholder
	}
	draft := strings.TrimSpace(q.CurrentDraft())
	if draft == "" {
		return placeholder
	}
	return truncateAsyncQuestionPreview(draft)
}

// AnswerText mirrors Rust's AsyncQuestions::go_next_or_submit text selection:
// the focused suggested option's label, or the trimmed live draft when the
// editable Other choice (or a free-text question) is focused. The caller passes
// the composer's current text because Go keeps one live composer. A blank draft
// is not a ready answer.
func (q *AsyncQuestions) AnswerText(draft string) (string, bool) {
	if q == nil {
		return "", false
	}
	if q.FocusIsNotes() {
		text := strings.TrimSpace(draft)
		if text == "" {
			return "", false
		}
		return text, true
	}
	index := q.SelectedOptionIndex()
	question, ok := q.CurrentQuestion()
	if !ok || index < 0 || index >= len(question.Options) {
		return "", false
	}
	return question.Options[index], true
}

// AnswerIsNamedChoice reports whether the pending answer is a suggested option
// rather than custom text; only named choices require the visibility guard.
func (q *AsyncQuestions) AnswerIsNamedChoice() bool {
	return q != nil && q.HasOptions() && !q.OtherSelected()
}

// truncateAsyncQuestionPreview bounds the Other row preview like Rust: 512
// bytes at a rune boundary, then 128 graphemes (approximated by runes).
func truncateAsyncQuestionPreview(text string) string {
	if len(text) > asyncQuestionMaxOptionBytes {
		end := asyncQuestionMaxOptionBytes
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		text = text[:end]
	}
	runes := []rune(text)
	if len(runes) > 128 {
		runes = runes[:128]
	}
	return string(runes)
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
	if q.expanded {
		q.SnoozeAutoResolution()
	}
}

// SnoozeAutoResolution clears the collapsed countdown for every pending
// question, mirroring Rust's `snooze_auto_resolution`: opening or using the
// editor stops the countdown.
func (q *AsyncQuestions) SnoozeAutoResolution() {
	if q == nil {
		return
	}
	for index := range q.pending {
		q.pending[index].ExpiresAt = time.Time{}
	}
}

// TimerRemaining returns the shortest positive countdown remaining, or zero
// when no question has an active countdown.
func (q *AsyncQuestions) TimerRemaining(now time.Time) time.Duration {
	if q == nil {
		return 0
	}
	remaining := time.Duration(0)
	for _, question := range q.pending {
		if question.ExpiresAt.IsZero() {
			continue
		}
		left := question.ExpiresAt.Sub(now)
		if left <= 0 {
			continue
		}
		if remaining == 0 || left < remaining {
			remaining = left
		}
	}
	return remaining
}

// Countdown renders the collapsed countdown ("12s") once at most 20 seconds
// remain, matching Rust's `countdown`.
func (q *AsyncQuestions) Countdown(now time.Time) (string, bool) {
	remaining := q.TimerRemaining(now)
	if remaining <= 0 || remaining > asyncQuestionCountdownVisibleAfter {
		return "", false
	}
	seconds := int((remaining + time.Second - 1) / time.Second)
	return strconv.Itoa(seconds) + "s", true
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

// BuildAsyncQuestionAnswer mirrors Rust's AsyncQuestions::go_next_or_submit
// (#46486): trim the draft, render the answer through the desktop reply
// envelope with the question's stable identity, and reject the whole reply when
// it exceeds the user-input character limit (JSON escaping included). It
// returns ready=false when the answer is empty.
func BuildAsyncQuestionAnswer(questionID string, question AsyncUserInputQuestion, draft string) (reply string, ready bool, tooLong bool) {
	text := strings.TrimSpace(draft)
	if text == "" {
		return "", false, false
	}
	reply = context.Render(context.NewAnsweredQuestion(questionID, question.Title, text)).Content
	if utf8.RuneCountInString(reply) > turn.MaxUserInputTextChars {
		return "", false, true
	}
	return reply, true, false
}
