package tea

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

// TestAsyncQuestionChoicesRenderMarkdownLikeRust mirrors Rust #57259: named
// suggestions keep their Markdown styling and terminal hyperlinks while the
// numbered prefix stays in the gutter.
func TestAsyncQuestionChoicesRenderMarkdownLikeRust(t *testing.T) {
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-markdown", []any{
		map[string]any{
			"title":   "Which database?",
			"options": []any{"*Postgres*", "[guide](https://example.com/docs)"},
		},
	})
	model.expandAsyncQuestions()
	view := model.View()
	if !strings.Contains(view, "\x1b[3m") {
		t.Fatalf("choice lost italic Markdown styling:\n%q", view)
	}
	if !strings.Contains(view, "\x1b]8;;https://example.com/docs\x07") {
		t.Fatalf("choice lost its terminal hyperlink:\n%q", view)
	}
	plain := codextui.StripOSC8(utils.StripANSI(view))
	if !strings.Contains(plain, "› 1. Postgres") || !strings.Contains(plain, "guide") {
		t.Fatalf("choice text lost its numbered row:\n%s", plain)
	}
	question, ok := model.asyncQuestions.CurrentQuestion()
	if !ok {
		t.Fatal("missing current question")
	}
	rows := model.asyncQuestionBlockRows(question)
	if rows < 4 {
		t.Fatalf("block rows = %d, want the rendered Markdown choices measured", rows)
	}
}

// TestAsyncQuestionExpansionDisablesMouseCaptureLikeRust mirrors Rust #57285
// on top of the explicit policy from #57284: expanded questions hand link
// gestures back to the terminal only on an owned screen, suspend that policy
// for an active modal, and restore capture when the questions collapse.
func TestAsyncQuestionExpansionDisablesMouseCaptureLikeRust(t *testing.T) {
	owned := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40, AlternateScreenMode: "always"})
	owned.State.SetThreadID("thread-questions")
	owned = feedAsyncQuestions(t, owned, "question-mouse", []any{
		map[string]any{"title": "Which database?", "options": []any{"Postgres"}},
	})
	if cmd := owned.syncMouseCapture(); cmd != nil || !owned.mouseCaptureActive {
		t.Fatal("collapsed questions must keep mouse capture")
	}
	owned.expandAsyncQuestions()
	if cmd := owned.syncMouseCapture(); cmd == nil || owned.mouseCaptureActive {
		t.Fatal("expanded questions must hand mouse capture back to the terminal")
	}
	if owned.mouseCapturePolicy != mouseCaptureDisabled {
		t.Fatalf("policy = %v, want disabled", owned.mouseCapturePolicy)
	}

	// An active modal keeps capture so the overlay stays interactive.
	owned.modal = &modalState{}
	if cmd := owned.syncMouseCapture(); cmd == nil || !owned.mouseCaptureActive {
		t.Fatal("an active modal must restore mouse capture")
	}
	owned.modal = nil
	if cmd := owned.syncMouseCapture(); cmd == nil || owned.mouseCaptureActive {
		t.Fatal("closing the modal must disable capture again")
	}

	owned.collapseAsyncQuestions()
	if cmd := owned.syncMouseCapture(); cmd == nil || !owned.mouseCaptureActive {
		t.Fatal("collapsed questions must restore mouse capture")
	}

	// Inline mode never captures the mouse, so the policy has nothing to change.
	inline := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40, NoAltScreen: true})
	inline.State.SetThreadID("thread-inline")
	inline = feedAsyncQuestions(t, inline, "question-inline", []any{map[string]any{"title": "Inline?"}})
	inline.expandAsyncQuestions()
	if cmd := inline.syncMouseCapture(); cmd != nil || inline.mouseCaptureActive {
		t.Fatal("inline mode must leave mouse handling to the terminal")
	}
	if inline.mouseCapturePolicy != mouseCaptureInherit {
		t.Fatalf("inline policy = %v, want inherit", inline.mouseCapturePolicy)
	}
}

// TestMouseCapturePolicyPrecedence mirrors Rust #57284: Inherit follows screen
// ownership while Enabled/Disabled are absolute, and transcript overlays enable
// capture even inline while static pagers hand gestures back to the terminal.
func TestMouseCapturePolicyPrecedence(t *testing.T) {
	cases := []struct {
		policy mouseCapturePolicy
		owned  bool
		want   bool
	}{
		{mouseCaptureInherit, true, true},
		{mouseCaptureInherit, false, false},
		{mouseCaptureEnabled, true, true},
		{mouseCaptureEnabled, false, true},
		{mouseCaptureDisabled, true, false},
		{mouseCaptureDisabled, false, false},
	}
	for _, testCase := range cases {
		if got := testCase.policy.captures(testCase.owned); got != testCase.want {
			t.Fatalf("policy %v owned=%v captures=%v, want %v", testCase.policy, testCase.owned, got, testCase.want)
		}
	}

	inline := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30, NoAltScreen: true})
	inline.overlay = chatwidget.NewTranscriptOverlay(100, 30, "alpha\nbeta")
	inline.overlayTranscript = true
	inline.overlayMouseCapture = mouseCaptureEnabled
	if cmd := inline.syncMouseCapture(); cmd == nil || !inline.mouseCaptureActive {
		t.Fatal("transcript overlay must enable capture inline")
	}
	inline.overlay = nil
	inline.overlayTranscript = false
	inline.overlayMouseCapture = mouseCaptureInherit
	if cmd := inline.syncMouseCapture(); cmd == nil || inline.mouseCaptureActive {
		t.Fatal("closing the overlay must restore inherit for an inline screen")
	}
}

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

// TestAsyncQuestionsNotifyLikeRust mirrors Rust #46574: newly unanswered
// questions post an async-question notification, using a shortened single-question
// title or a batch count, while duplicates and replayed history stay silent.
func TestAsyncQuestionsNotifyLikeRust(t *testing.T) {
	var posted []string
	model := NewModel(codextui.NewState(nil), Options{
		Width: 120, Height: 40,
		// Notifications default to unfocused-only; the focused test terminal
		// needs the always condition to observe the post.
		NotificationCondition: codextui.NotificationConditionAlways,
		OnPostNotification: func(message string, _ codextui.NotificationMethod) bubbletea.Cmd {
			posted = append(posted, message)
			return nil
		},
	})
	model.State.SetThreadID("thread-questions")

	model = feedAsyncQuestions(t, model, "question-single", []any{
		map[string]any{"title": "Which database?"},
	})
	if len(posted) != 1 || posted[0] != "Question: Which database?" {
		t.Fatalf("single question notifications = %#v", posted)
	}

	// A duplicate delivery (same message id) adds no question and stays silent.
	model = feedAsyncQuestions(t, model, "question-single", []any{
		map[string]any{"title": "Which database?"},
	})
	if len(posted) != 1 {
		t.Fatalf("duplicate delivery notified again: %#v", posted)
	}

	// A batch reports its added count.
	model = feedAsyncQuestions(t, model, "question-batch", []any{
		map[string]any{"title": "First?"},
		map[string]any{"title": "Second?"},
	})
	if len(posted) != 2 || posted[1] != "Question: 2 questions requested" {
		t.Fatalf("batch notifications = %#v", posted)
	}

	// A long title is truncated to 30 graphemes with an ellipsis.
	long := strings.Repeat("x", 40)
	model = feedAsyncQuestions(t, model, "question-long", []any{
		map[string]any{"title": long},
	})
	if len(posted) != 3 || posted[2] != "Question: "+strings.Repeat("x", 27)+"..." {
		t.Fatalf("long-title notifications = %#v", posted)
	}
}

// TestAsyncQuestionNotificationTitleFallbacksLikeRust covers the title rule's
// fallbacks that the parsed-question flow cannot reach (empty titles are
// dropped when parsing).
func TestAsyncQuestionNotificationTitleFallbacksLikeRust(t *testing.T) {
	if got := asyncQuestionNotificationTitle(nil, 1); got != "Question requested" {
		t.Fatalf("single-added fallback = %q", got)
	}
	if got := asyncQuestionNotificationTitle(nil, 3); got != "3 questions requested" {
		t.Fatalf("batch fallback = %q", got)
	}
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
	if !strings.Contains(view, "2 questions") || !strings.Contains(view, "shift+← to answer") {
		t.Fatalf("collapsed summary missing:\n%s", view)
	}
	if strings.Contains(view, "1 of 2") {
		t.Fatalf("collapsed view must not render the editor:\n%s", view)
	}
}

// TestAsyncQuestionHintsFollowResolvedShortcutLikeRust mirrors Rust #47618:
// question footers use the first configured shortcut for each action, render
// custom chords, and omit an unbound action.
func TestAsyncQuestionHintsFollowResolvedShortcutLikeRust(t *testing.T) {
	config := codextui.NewKeymapConfig()
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40, KeymapConfig: config})
	model = feedAsyncQuestions(t, model, "question-1", []any{
		map[string]any{"title": "Which database?"},
		map[string]any{"title": "Deadline?"},
	})
	if view := model.View(); !strings.Contains(view, "shift+← to answer") {
		t.Fatalf("default question hint missing:\n%s", view)
	}
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyShiftLeft})
	model = updated.(*Model)
	if view := model.View(); !strings.Contains(view, "shift+→ main prompt") || !strings.Contains(view, "shift+← next question") {
		t.Fatalf("default question footer missing:\n%s", view)
	}

	config = codextui.NewKeymapConfig()
	if err := config.Set("chat", "edit_queued_message", []string{"alt-up", "shift-left"}); err != nil {
		t.Fatalf("set edit_queued_message: %v", err)
	}
	if err := config.Set("chat", "prompt_stack_back", []string{"alt-down", "shift-right"}); err != nil {
		t.Fatalf("set prompt_stack_back: %v", err)
	}
	model = NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40, KeymapConfig: config})
	model = feedAsyncQuestions(t, model, "question-1", []any{map[string]any{"title": "Which database?"}})
	if view := model.View(); !strings.Contains(view, "alt+↑ to answer") {
		t.Fatalf("configured question hint missing:\n%s", view)
	}

	config = codextui.NewKeymapConfig()
	if err := config.Set("chat", "edit_queued_message", []string{"ctrl-x up", "shift-left"}); err != nil {
		t.Fatalf("set chord edit_queued_message: %v", err)
	}
	if err := config.Set("chat", "prompt_stack_back", []string{"ctrl-x down", "shift-right"}); err != nil {
		t.Fatalf("set chord prompt_stack_back: %v", err)
	}
	model = NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40, KeymapConfig: config})
	model = feedAsyncQuestions(t, model, "question-1", []any{
		map[string]any{"title": "Which database?"},
		map[string]any{"title": "Deadline?"},
	})
	if view := model.View(); !strings.Contains(view, "ctrl+x ↑ to answer") {
		t.Fatalf("chord question hint missing:\n%s", view)
	}
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyShiftLeft})
	model = updated.(*Model)
	if view := model.View(); !strings.Contains(view, "ctrl+x ↓ main prompt") || !strings.Contains(view, "ctrl+x ↑ next question") {
		t.Fatalf("chord question footer missing:\n%s", view)
	}

	config = codextui.NewKeymapConfig()
	if err := config.Set("chat", "edit_queued_message", []string{}); err != nil {
		t.Fatalf("unbind edit_queued_message: %v", err)
	}
	model = NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40, KeymapConfig: config})
	model = feedAsyncQuestions(t, model, "question-1", []any{map[string]any{"title": "Which database?"}})
	if view := model.View(); strings.Contains(view, "to answer") {
		t.Fatalf("unbound question hint must be hidden:\n%s", view)
	}
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	if model.asyncQuestions.Expanded() {
		t.Fatal("unbound edit action must not expand questions through a fallback")
	}
}

// asyncQuestionReplyPrompt renders the desktop reply envelope a submitted
// answer carries (Rust #46486), including the stable per-question identity the
// desktop uses: JSON.stringify(["request_user_input_async", message id, index]).
func asyncQuestionReplyPrompt(messageID string, index int, question string, answer string) string {
	identity, _ := json.Marshal([]any{"request_user_input_async", messageID, index})
	payload, _ := json.Marshal([]map[string]string{{
		"answer":         answer,
		"question":       question,
		"questionItemId": string(identity),
	}})
	return "<send_user_message_question_reply>\n" + string(payload) + "\n</send_user_message_question_reply>"
}

// TestAsyncQuestionsAnsweredOnAnotherClientDismissesLikeRust mirrors Rust
// #46486: a reply envelope committed by another client dismisses the question it
// names even when another pending question has an identical title, and the
// draft of the surviving question is preserved.
func TestAsyncQuestionsAnsweredOnAnotherClientDismissesLikeRust(t *testing.T) {
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-1", []any{
		map[string]any{"title": "Which database?"},
		map[string]any{"title": "Which database?"},
	})
	// Another client answers the first question by its stable identity, so the
	// identical title of the second question must not match it.
	updated, _ := model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
		Type: "item.completed",
		Item: &protocol.ThreadItem{
			ID:   "message-2",
			Type: "user_message",
			Text: asyncQuestionReplyPrompt("question-1", 0, "Which database?", "Postgres"),
		},
	}})
	model = updated.(*Model)

	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatalf("pending = %d, want only the unanswered identical-title question", model.asyncQuestions.UnansweredCount())
	}
	if question, ok := model.asyncQuestions.CurrentQuestion(); !ok || question.Title != "Which database?" {
		t.Fatalf("current question = %#v, %v", question, ok)
	}
	// The envelope must not leak into the rendered transcript.
	if view := model.View(); strings.Contains(view, "<send_user_message_question_reply>") {
		t.Fatalf("the reply envelope leaked into the transcript:\n%s", view)
	}
}

// TestAsyncQuestionMarkdownMatchesLoadedHistoryLikeRust mirrors Rust #57292:
// live and loaded async agent messages decorate the same Markdown source with
// accent question prefixes and render the suggestions as that question's body.
func TestAsyncQuestionMarkdownMatchesLoadedHistoryLikeRust(t *testing.T) {
	questions := []any{
		map[string]any{"title": "Keep `--verbose` for **failed** runs?", "options": []any{"Only for **CI**"}},
		map[string]any{"title": "See the [guide](https://example.test/guide)?"},
	}
	item := &protocol.ThreadItem{
		ID:       "question-markdown-history",
		Type:     "agent_message",
		Text:     "Keep `--verbose` for **failed** runs?\n- Only for **CI**\n\nSee the [guide](https://example.test/guide)?",
		Delivery: "async",
		Metadata: map[string]any{"questions": questions},
	}
	model := newAsyncQuestionModel()
	updated, _ := model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "item.completed", Item: item}})
	model = updated.(*Model)

	var live codextui.Message
	for _, message := range model.State.Messages {
		if message.Role == codextui.RoleAssistant {
			live = message
		}
	}
	if len(live.AsyncQuestions) != 2 {
		t.Fatalf("live async questions = %#v", live.AsyncQuestions)
	}
	loaded := applyBufferedThreadItemToMessages(nil, item)
	if len(loaded) != 1 || len(loaded[0].AsyncQuestions) != 2 {
		t.Fatalf("loaded messages = %#v", loaded)
	}

	liveLines := transcriptMessageDisplayLines(live, 80, "", false, "", false)
	loadedLines := transcriptMessageDisplayLines(loaded[0], 80, "", false, "", false)
	if strings.Join(liveLines, "\n") != strings.Join(loadedLines, "\n") {
		t.Fatalf("live and loaded rendering differ:\nlive:\n%s\nloaded:\n%s",
			utils.StripANSI(strings.Join(liveLines, "\n")), utils.StripANSI(strings.Join(loadedLines, "\n")))
	}
	plain := utils.StripANSI(strings.Join(liveLines, "\n"))
	for _, want := range []string{"? Keep --verbose for failed runs?", "• Only for CI", "? See the guide"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("question rendering missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "**") || strings.Contains(plain, "`") {
		t.Fatalf("question rendering leaked Markdown:\n%s", plain)
	}
}

// TestBufferedUserMessageRendersReplyEnvelopeAsTextLikeRust mirrors Rust
// #46486's replay rendering: a buffered reply envelope becomes readable
// question-and-answer text in the replayed transcript.
func TestBufferedUserMessageRendersReplyEnvelopeAsTextLikeRust(t *testing.T) {
	item := &protocol.ThreadItem{
		ID:   "message-2",
		Type: "user_message",
		Text: asyncQuestionReplyPrompt("question-1", 0, "Which database?", "Postgres"),
	}
	messages := applyBufferedThreadItemToMessages(nil, item)
	if len(messages) != 1 || messages[0].Role != codextui.RoleUser {
		t.Fatalf("buffered messages = %#v", messages)
	}
	if messages[0].Text != "> Which database?\n\nPostgres" {
		t.Fatalf("buffered reply text = %q", messages[0].Text)
	}
	// Ordinary user messages are unchanged.
	plain := applyBufferedThreadItemToMessages(nil, &protocol.ThreadItem{ID: "m", Type: "user_message", Text: "hello"})
	if len(plain) != 1 || plain[0].Text != "hello" {
		t.Fatalf("plain buffered messages = %#v", plain)
	}
}

// TestBufferedUserMessageRendersReplyMarkdownLikeRust mirrors Rust #57292:
// loaded transcripts use the same caption + Markdown answer surface as live
// replies.
func TestBufferedUserMessageRendersReplyMarkdownLikeRust(t *testing.T) {
	item := &protocol.ThreadItem{
		ID:   "message-3",
		Type: "user_message",
		Text: asyncQuestionReplyPrompt("question-1", 0, "*Which* database?", "Use **Postgres**."),
	}
	messages := applyBufferedThreadItemToMessages(nil, item)
	if len(messages) != 1 || len(messages[0].AsyncQuestionReplies) != 1 {
		t.Fatalf("buffered reply messages = %#v", messages)
	}
	lines := transcriptMessageDisplayLines(messages[0], 60, "", false, "", false)
	plain := utils.StripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(plain, "\u21aa Re: Which database?") {
		t.Fatalf("reply caption missing:\n%s", plain)
	}
	if !strings.Contains(plain, "Use Postgres.") {
		t.Fatalf("reply answer missing:\n%s", plain)
	}
}

// TestAsyncQuestionsAnsweredOnAnotherClientKeepsOtherDraftsLikeRust covers the
// cross-client dismissal preserving drafts for the remaining questions.
func TestAsyncQuestionsAnsweredOnAnotherClientKeepsOtherDraftsLikeRust(t *testing.T) {
	model := newAsyncQuestionModel()
	model = feedAsyncQuestions(t, model, "question-1", []any{
		map[string]any{"title": "First question?"},
		map[string]any{"title": "Second question?"},
	})
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	// Alt+Up again moves to the second question; draft it, then answer the first
	// question from another client.
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	if index := model.asyncQuestions.CurrentIndex(); index != 1 {
		t.Fatalf("focused question = %d, want the second question", index)
	}
	for _, r := range "kept draft" {
		updated, _ = model.Update(keyRunes(r))
		model = updated.(*Model)
	}
	model.resolveAsyncQuestionsFromReplyText(asyncQuestionReplyPrompt("question-1", 0, "First question?", "Answered"))
	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatalf("pending = %d, want the second question", model.asyncQuestions.UnansweredCount())
	}
	if question, ok := model.asyncQuestions.CurrentQuestion(); !ok || question.Title != "Second question?" {
		t.Fatalf("current question = %#v, %v", question, ok)
	}
	if got := model.composer.Value(); got != "kept draft" {
		t.Fatalf("composer = %q, want the preserved draft", got)
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

	typeText(t, model, "Postgres")
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)

	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != asyncQuestionReplyPrompt("question-1", 0, "Which database?", "Postgres") {
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
	for _, want := range []string{"› 1. Postgres", "  2. SQLite", "  3. Other", "enter submit"} {
		if !strings.Contains(view, want) {
			t.Fatalf("choice view missing %q:\n%s", want, view)
		}
	}

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEnter})
	model = updated.(*Model)
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].Prompt != asyncQuestionReplyPrompt("question-1", 0, "Which database?", "Postgres") {
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
	if requests := model.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != asyncQuestionReplyPrompt("question-1", 0, "Which database?", "SQLite") {
		t.Fatalf("submitted requests = %#v", requests)
	}
}

// TestAsyncQuestionsDigitShortcutSubmitsNamedChoice covers Rust #42894's digit
// shortcuts submitting a named choice directly.
func TestAsyncQuestionsDigitShortcutSubmitsNamedChoice(t *testing.T) {
	model := withChoiceQuestion(t)
	updated, _ := model.Update(keyRunes('2'))
	model = updated.(*Model)
	if requests := model.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != asyncQuestionReplyPrompt("question-1", 0, "Which database?", "SQLite") {
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
	if requests := model.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != asyncQuestionReplyPrompt("question-1", 0, "Which database?", "k") {
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
	if requests := roomy.SubmittedRequests(); len(requests) != 1 || requests[0].Prompt != asyncQuestionReplyPrompt("question-1", 0, "Which database should we deploy to production?", "Postgres") {
		t.Fatalf("roomy submitted requests = %#v", requests)
	}
}

// TestAsyncQuestionsCollapsedCountdownAppearsAndSnoozes covers Rust #42903: a
// newly arrived collapsed question shows a countdown in its last 20 seconds and
// opening the editor stops it.
func TestAsyncQuestionsCollapsedCountdownAppearsAndSnoozes(t *testing.T) {
	now := fixedTeaTime()
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-questions")
	model.now = func() time.Time { return now }

	model = feedAsyncQuestions(t, model, "question-1", []any{map[string]any{"title": "Which?"}})
	if strings.Contains(model.View(), " · 30s") {
		t.Fatalf("countdown should be hidden with 30 seconds left:\n%s", model.View())
	}

	now = now.Add(15 * time.Second)
	if view := model.View(); !strings.Contains(view, " · 15s") {
		t.Fatalf("countdown missing:\n%s", view)
	}

	// Opening the editor snoozes the countdown for every pending question.
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	model = updated.(*Model)
	if view := model.View(); strings.Contains(view, " · 15s") {
		t.Fatalf("countdown must clear after the editor is opened:\n%s", view)
	}
}

// TestAsyncQuestionsForwardNavigationRestoresQueuedMessage covers Rust #42903:
// forward navigation from the last question collapses the editor and restores
// the latest queued message as the main composer draft.
func TestAsyncQuestionsForwardNavigationRestoresQueuedMessage(t *testing.T) {
	model := newAsyncQuestionModel()
	model.composer.SetValue("queued follow-up")
	model.queueComposer(false)
	if len(model.queued) != 1 {
		t.Fatalf("seeded queue = %d, want 1", len(model.queued))
	}
	model = feedAsyncQuestions(t, model, "question-1", []any{map[string]any{"title": "Which?"}})

	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	if !model.asyncQuestions.Expanded() {
		t.Fatal("Alt+Up must focus the question editor")
	}
	updated, _ = model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)

	if model.asyncQuestions.Expanded() {
		t.Fatal("forward navigation past the last question must collapse the editor")
	}
	if got := model.composer.Value(); got != "queued follow-up" {
		t.Fatalf("composer = %q, want the restored queued message", got)
	}
	if len(model.queued) != 0 {
		t.Fatalf("queue = %#v, want the restored message removed", model.queued)
	}
	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatal("restoring a queued message must not consume the question")
	}
}

// TestAsyncQuestionsBufferedLiveQuestionsAppearOnAgentSwitch covers Rust #42903:
// buffered live notifications from a background subagent introduce questions
// when switching to that thread.
func TestAsyncQuestionsBufferedLiveQuestionsAppearOnAgentSwitch(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-main")
	model := NewModel(state, Options{
		OnSwitchAgent: func(threadID string) (AgentThreadSwitchResponse, error) {
			return AgentThreadSwitchResponse{
				Entry:    codextui.AgentThreadEntry{ThreadID: threadID, AgentNickname: "Scout", AgentRole: "worker"},
				Messages: nil,
				Status:   "running",
			}, nil
		},
	})
	model.Update(ThreadScopedEventMsg{ThreadID: "thread-worker", Event: protocol.ThreadEvent{
		Type: "item.completed",
		Item: asyncQuestionItem("question-buffered", []any{map[string]any{"title": "Buffered?"}}),
	}})
	if model.asyncQuestions.UnansweredCount() != 0 {
		t.Fatal("questions from an inactive thread must not surface before the switch")
	}

	model.Update(AgentSwitchResultMsg{
		ThreadID: "thread-worker",
		Response: AgentThreadSwitchResponse{
			Entry:    codextui.AgentThreadEntry{ThreadID: "thread-worker", AgentNickname: "Scout", AgentRole: "worker"},
			Status:   "running",
			Messages: nil,
		},
	})
	if model.asyncQuestions.UnansweredCount() != 1 {
		t.Fatalf("questions after switch = %d, want 1", model.asyncQuestions.UnansweredCount())
	}
	question, ok := model.asyncQuestions.CurrentQuestion()
	if !ok || question.Title != "Buffered?" {
		t.Fatalf("buffered question = %#v (ok=%v)", question, ok)
	}
}

// TestAsyncQuestionTitleLinksWrappedURLsLikeRust mirrors Rust #51391: a web URL
// in an asynchronous question title is rendered as a terminal hyperlink whose
// complete destination survives wrapping.
func TestAsyncQuestionTitleLinksWrappedURLsLikeRust(t *testing.T) {
	url := "https://github.com/openai/codex/pull/12345?diff=split"
	title := "Review this change (" + url + ")?"
	model := NewModel(codextui.NewState(nil), Options{Width: 40, Height: 40})
	model.State.SetThreadID("thread-question-url")
	model = feedAsyncQuestions(t, model, "question-url", []any{map[string]any{"title": title}})
	updated, _ := model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyUp, Alt: true})
	model = updated.(*Model)
	if !model.asyncQuestions.Expanded() {
		t.Fatal("Alt+Up must focus the question editor")
	}
	view := model.View()
	if linked := linkedTextForDestination(t, view, url); linked != url {
		t.Fatalf("linked text = %q, want the complete URL %q", linked, url)
	}

	// The question area wraps the URL across rows; a clipped render keeps only
	// a leading fragment, which still carries the complete destination.
	lines := wrapAsyncQuestionTitleLines(title, 38)
	if len(lines) < 3 {
		t.Fatalf("wrapped title = %#v, want the URL to span rows", lines)
	}
	for _, line := range lines {
		if width := codextui.DisplayWidth(codextui.StripOSC8(line)); width > 38 {
			t.Fatalf("wrapped row %q is %d columns, want <= 38", codextui.StripOSC8(line), width)
		}
	}
	gotLines := make([]string, len(lines))
	for index, line := range lines {
		gotLines[index] = codextui.StripOSC8(line)
	}
	wantLines := []string{
		"Review this change (https://",
		"github.com/openai/codex/pull/12345?",
		"diff=split)?",
	}
	if got, want := strings.Join(gotLines, "\n"), strings.Join(wantLines, "\n"); got != want {
		t.Fatalf("wrapped title =\n%s\nwant\n%s", got, want)
	}
	clipped := strings.Join(lines[:min(2, len(lines))], "\n")
	linked := linkedTextForDestination(t, clipped, url)
	if linked == "" || !strings.HasPrefix(url, linked) {
		t.Fatalf("clipped linked text = %q, want a prefix of %q", linked, url)
	}
}

func linkedTextForDestination(t *testing.T, rendered string, destination string) string {
	t.Helper()
	linked := ""
	for _, line := range strings.Split(rendered, "\n") {
		for _, link := range osc8LinksInLine(line) {
			if link.destination != destination {
				t.Fatalf("hyperlink destination = %q, want %q", link.destination, destination)
			}
			linked += link.text
		}
	}
	return linked
}
