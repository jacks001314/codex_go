package chatwidget

import (
	"reflect"
	"strings"
	"testing"

	idecontext "codex_go/tui/ide_context"
	"codex_go/turn"
)

// TestUserMessageReplyEnvelopeRendersAsTextLikeRust mirrors Rust #46486: a
// stored desktop reply envelope renders as readable question-and-answer text in
// the transcript, previews, and restored history, while an explicit override
// still wins.
func TestUserMessageReplyEnvelopeRendersAsTextLikeRust(t *testing.T) {
	envelope := "<send_user_message_question_reply>\n" +
		`[{"answer":"Postgres","question":"Which database?","questionItemId":"[\"request_user_input_async\",\"message\",0]"}]` +
		"\n</send_user_message_question_reply>"
	message := UserMessage{Text: envelope, TextElements: []turn.TextElement{{ByteRange: turn.ByteRange{Start: 0, End: 4}}}}

	// An authentic committed message is rendered as stored (Rust's
	// user_message_display_for_history keeps UserMessageText unchanged); the
	// readable text comes from the restore and preview paths below.
	if display := UserMessageDisplayForHistory(message, UserMessageTextHistoryRecord()); display.Message != envelope {
		t.Fatalf("committed display text = %q", display.Message)
	}
	restored := UserMessageForRestore(message, UserMessageTextHistoryRecord())
	if restored.Text != "> Which database?\n\nPostgres" || len(restored.TextElements) != 0 {
		t.Fatalf("restored message = %#v", restored)
	}
	restoredOverride := UserMessageForRestore(message, UserMessageOverrideHistoryRecord(""))
	if restoredOverride.Text != "> Which database?\n\nPostgres" || len(restoredOverride.TextElements) != 0 {
		t.Fatalf("empty-override restore = %#v", restoredOverride)
	}
	if got := UserMessagePreviewText(message, nil); got != "> Which database?\n\nPostgres" {
		t.Fatalf("preview text = %q", got)
	}
	// An override with text still wins over the envelope rendering.
	override := UserMessageOverrideHistoryRecord("edited prompt")
	if got := UserMessagePreviewText(message, &override); got != "edited prompt" {
		t.Fatalf("override preview = %q", got)
	}
	if restored := UserMessageForRestore(message, override); restored.Text != "edited prompt" {
		t.Fatalf("override restore = %#v", restored)
	}
	// An ordinary message is untouched.
	plain := UserMessage{Text: "hello"}
	if got := UserMessagePreviewText(plain, nil); got != "hello" {
		t.Fatalf("plain preview = %q", got)
	}
	if display := UserMessageDisplayForHistory(plain, UserMessageTextHistoryRecord()); display.Message != "hello" {
		t.Fatalf("plain display = %q", display.Message)
	}
}

func TestUserMessagePreviewText(t *testing.T) {
	message := UserMessage{
		Text:            "core text",
		LocalImages:     []string{"one.png"},
		RemoteImageURLs: []string{"https://example.test/one.png"},
	}
	override := UserMessageOverrideHistoryRecord("history text")
	if got := UserMessagePreviewText(message, &override); got != "history text" {
		t.Fatalf("override preview = %q", got)
	}
	spaceOverride := UserMessageOverrideHistoryRecord("   ")
	if got := UserMessagePreviewText(message, &spaceOverride); got != "   " {
		t.Fatalf("space override preview = %q", got)
	}
	if got := UserMessagePreviewText(message, nil); got != "core text" {
		t.Fatalf("text preview = %q", got)
	}
	message.Text = ""
	message.RemoteImageURLs = nil
	if got := UserMessagePreviewText(message, nil); got != "" {
		t.Fatalf("local image preview should match Rust empty text, got %q", got)
	}
	message.LocalImages = nil
	message.RemoteImageURLs = []string{"a", "b"}
	if got := UserMessagePreviewText(message, nil); got != "" {
		t.Fatalf("remote image preview should match Rust empty text, got %q", got)
	}
}

// TestUserMessageDisplayFromInputsDecodesTaskLinksLikeRust mirrors Rust
// chatwidget::user_message_display: a stored task link covered by its mention
// element displays as `@title`, while a bare link without the element is left
// as-is.
func TestUserMessageDisplayFromInputsDecodesTaskLinksLikeRust(t *testing.T) {
	title := "Review the migration"
	link := "[@" + title + "](thread://task-123)"
	placeholder := "@" + title
	display := UserMessageDisplayFromInputs([]turn.TurnUserInput{{
		Type: "text",
		Text: link + " next",
		TextElements: []turn.TextElement{{
			ByteRange:   turn.ByteRange{Start: 0, End: uint(len(link))},
			Placeholder: &placeholder,
		}},
	}})
	if display.Message != "@"+title+" next" {
		t.Fatalf("decoded display = %q", display.Message)
	}
	if len(display.TextElements) != 1 {
		t.Fatalf("decoded elements = %#v", display.TextElements)
	}
	element := display.TextElements[0]
	if element.Placeholder == nil || *element.Placeholder != placeholder {
		t.Fatalf("placeholder = %#v", element.Placeholder)
	}
	if got := display.Message[element.ByteRange.Start:element.ByteRange.End]; got != placeholder {
		t.Fatalf("element range = %q", got)
	}
	literal := UserMessageDisplayFromInputs([]turn.TurnUserInput{{Type: "text", Text: link}})
	if literal.Message != link {
		t.Fatalf("literal display = %q", literal.Message)
	}
}

func TestThreadComposerStateHasContent(t *testing.T) {
	if (ThreadComposerState{}).HasContent() {
		t.Fatal("empty composer should not have content")
	}
	cases := []ThreadComposerState{
		{Text: "hello"},
		{Text: "   "},
		{LocalImages: []string{"image.png"}},
		{RemoteImageURLs: []string{"https://example.test/image.png"}},
		{TextElements: []turn.TextElement{{ByteRange: turn.ByteRange{Start: 0, End: 1}}}},
		{MentionBindings: []string{"@file"}},
		{PendingPastes: [][2]string{{"[Pasted Content 3 chars]", "abc"}}},
	}
	for _, tc := range cases {
		if !tc.HasContent() {
			t.Fatalf("composer should have content: %#v", tc)
		}
	}
}

func TestInitialUserMessageKeepsWhitespaceAndImagePathsMatchRust(t *testing.T) {
	message, ok := InitialUserMessage("   ", nil, nil)
	if !ok || message.Text != "   " {
		t.Fatalf("whitespace initial message = %#v ok=%v", message, ok)
	}
	message, ok = InitialUserMessage("", []string{""}, nil)
	if !ok || !reflect.DeepEqual(message.LocalImages, []string{""}) {
		t.Fatalf("blank image path message = %#v ok=%v", message, ok)
	}
}

func TestMergeUserMessagesWithTextElements(t *testing.T) {
	elementPlaceholder := "[Image #1]"
	first := UserMessage{
		Text:         "look [Image #1]",
		LocalImages:  []string{"a.png"},
		TextElements: []turn.TextElement{{ByteRange: turn.ByteRange{Start: 5, End: 15}, Placeholder: &elementPlaceholder}},
	}
	second := UserMessage{
		Text:         "then [Image #1]",
		LocalImages:  []string{"b.png"},
		TextElements: []turn.TextElement{{ByteRange: turn.ByteRange{Start: 5, End: 15}, Placeholder: &elementPlaceholder}},
	}

	got := MergeUserMessages([]UserMessage{first, second})

	if got.Text != "look [Image #1]\nthen [Image #2]" {
		t.Fatalf("merged text = %q", got.Text)
	}
	if !reflect.DeepEqual(got.LocalImages, []string{"a.png", "b.png"}) {
		t.Fatalf("local images = %#v", got.LocalImages)
	}
	if len(got.TextElements) != 2 || got.TextElements[0].Placeholder == nil || *got.TextElements[0].Placeholder != "[Image #1]" {
		t.Fatalf("text elements = %#v", got.TextElements)
	}
	if len(got.TextElements) != 2 || got.TextElements[1].Placeholder == nil || *got.TextElements[1].Placeholder != "[Image #2]" {
		t.Fatalf("text elements = %#v", got.TextElements)
	}
}

func TestMergeUserMessagesWithHistoryRecord(t *testing.T) {
	first := UserMessageWithHistory(NewUserMessage("core one"), UserMessageTextHistoryRecord())
	second := UserMessageWithHistory(NewUserMessage("core two"), UserMessageOverrideHistoryRecord("history two"))

	message, record := MergeUserMessagesWithHistoryRecord([]messageWithHistoryRecord{first, second})

	if message.Text != "core one\ncore two" {
		t.Fatalf("merged message text = %q", message.Text)
	}
	if record.Kind != UserMessageHistoryOverride || record.Text != "core one\nhistory two" {
		t.Fatalf("history record = %#v", record)
	}
}

func TestUserMessageForRestoreUsesHistoryOverride(t *testing.T) {
	message := NewUserMessage("core text")
	record := UserMessageOverrideHistoryRecord("history text")
	got := UserMessageForRestore(message, record)
	if got.Text != "history text" {
		t.Fatalf("restore text = %q", got.Text)
	}
}

func TestPendingSteerCompareKeyFromItems(t *testing.T) {
	got := PendingSteerCompareKeyFromItems([]turn.TurnUserInput{
		{Type: "text", Text: "hello "},
		{Type: "mention", Name: "file"},
		{Type: "image", URL: "https://example.test/image.png"},
		{Type: "localImage", Path: "local.png"},
		{Type: "text", Text: "world"},
	})
	want := PendingSteerCompareKey{Message: "hello world", ImageCount: 2}
	if got != want {
		t.Fatalf("compare key = %#v, want %#v", got, want)
	}
}

func TestUserMessageDisplayFromInputs(t *testing.T) {
	got := UserMessageDisplayFromInputs([]turn.TurnUserInput{
		{Type: "text", Text: "hello"},
		{Type: "image", URL: "https://example.test/image.png"},
		{Type: "localImage", Path: "local.png"},
		{Type: "text", Text: " world"},
	})
	if got.Message != "hello world" ||
		!reflect.DeepEqual(got.RemoteImageURLs, []string{"https://example.test/image.png"}) ||
		!reflect.DeepEqual(got.LocalImages, []string{"local.png"}) {
		t.Fatalf("display = %#v", got)
	}
}

func TestUserMessageDisplayFromPartsExtractsPromptRequestMatchRust(t *testing.T) {
	placeholder := "visible"
	message := UserMessage{
		Text: "context " + idecontext.PromptRequestBegin + "\n  visible  ",
	}
	visibleStart := strings.Index(message.Text, "visible")
	message.TextElements = []turn.TextElement{
		{ByteRange: turn.ByteRange{Start: 0, End: 7}},
		{ByteRange: turn.ByteRange{Start: uint(visibleStart), End: uint(visibleStart + len("visible"))}, Placeholder: &placeholder},
	}
	got := UserMessageDisplayFromParts(message)
	if got.Message != "visible" {
		t.Fatalf("display message = %q", got.Message)
	}
	if len(got.TextElements) != 1 {
		t.Fatalf("display text elements = %#v", got.TextElements)
	}
	if got.TextElements[0].ByteRange.Start != 0 || got.TextElements[0].ByteRange.End != 7 {
		t.Fatalf("display text element range = %#v", got.TextElements[0].ByteRange)
	}
}
