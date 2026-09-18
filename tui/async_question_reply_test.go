package tui

import (
	"strings"
	"testing"

	"codex_go/turn"
)

// TestAsyncQuestionReplyAcceptssSingleAndBatchedEnvelopes mirrors Rust's
// async_question_reply_tests::desktop_question_reply_accepts_single_and_batched_envelopes.
func TestAsyncQuestionReplyAcceptssSingleAndBatchedEnvelopes(t *testing.T) {
	single := `{"questionItemId":"[\"request_user_input_async\",\"item\",0]","question":"Which environment?","answer":"Staging","extra":true}`
	for _, payload := range []string{single, "[" + single + "]"} {
		text := " \n<send_user_message_question_reply>\n" + payload + "\n</send_user_message_question_reply>\n "
		display, ok := AsyncQuestionReplyDisplayText(text)
		if !ok || display != "> Which environment?\n\nStaging" {
			t.Fatalf("display_text(%q) = %q, %v", text, display, ok)
		}
	}
	batched := `<send_user_message_question_reply>[{"questionItemId":"one","question":"First?","answer":"Yes"},{"questionItemId":"two","question":"Second?","answer":"No"}]</send_user_message_question_reply>`
	if display, ok := AsyncQuestionReplyDisplayText(batched); !ok || display != "> First?\n\nYes\n\n> Second?\n\nNo" {
		t.Fatalf("batched display = %q, %v", display, ok)
	}
	replies := ParseAsyncQuestionReplies(batched)
	if len(replies) != 2 || replies[0].QuestionItemID != "one" || replies[1].Answer != "No" {
		t.Fatalf("parsed replies = %#v", replies)
	}
}

// TestAsyncQuestionReplyRejectsMalformedOrEmbeddedEnvelopes mirrors Rust's
// malformed_or_embedded_question_envelopes_remain_ordinary_text.
func TestAsyncQuestionReplyRejectsMalformedOrEmbeddedEnvelopes(t *testing.T) {
	for _, text := range []string{
		"> Which environment?\n\nStaging",
		"<send_user_message_question_reply>[]</send_user_message_question_reply>",
		`<send_user_message_question_reply>[{"questionItemId":"one","question":"First?","answer":"Yes"},null]</send_user_message_question_reply>`,
		`Quoted: <send_user_message_question_reply>{"questionItemId":"one","question":"First?","answer":"Yes"}</send_user_message_question_reply>`,
		`<send_user_message_question_reply>{"questionItemId":"one","question":"First?","answer":"Yes"}</send_user_message_question_reply> trailing text`,
		`<send_user_message_question_reply>{"questionItemId":"one","question":"First?"}</send_user_message_question_reply>`,
	} {
		if replies := ParseAsyncQuestionReplies(text); replies != nil {
			t.Fatalf("parse(%q) = %#v, want nil", text, replies)
		}
		if display, ok := AsyncQuestionReplyDisplayText(text); ok {
			t.Fatalf("display_text(%q) = %q, want ordinary text", text, display)
		}
	}
}

// TestAsyncQuestionReplyFollowsIdeContextPrefix mirrors Rust's IDE-context
// handling: the envelope is read from the request section after the prefix.
func TestAsyncQuestionReplyFollowsIdeContextPrefix(t *testing.T) {
	text := "# Context from my IDE setup:\n\n## Open tabs:\n- main.go\n\n## My request for Codex:\n" +
		`<send_user_message_question_reply>{"questionItemId":"one","question":"First?","answer":"Yes"}</send_user_message_question_reply>`
	if display, ok := AsyncQuestionReplyDisplayText(text); !ok || display != "> First?\n\nYes" {
		t.Fatalf("IDE-context display = %q, %v", display, ok)
	}
	if replies := ParseAsyncQuestionReplies("# Context from my IDE setup:\nno delimiter"); replies != nil {
		t.Fatalf("missing delimiter parsed %#v", replies)
	}
}

// TestParseAsyncQuestionReplyInputRequiresOneTextItem mirrors Rust's parse_input:
// skill and mention items are ignored, and anything else must be a single text
// item.
func TestParseAsyncQuestionReplyInputRequiresOneTextItem(t *testing.T) {
	envelope := `<send_user_message_question_reply>{"questionItemId":"one","question":"First?","answer":"Yes"}</send_user_message_question_reply>`
	inputs := []turn.TurnUserInput{
		{Type: "skill", Path: "skill://demo"},
		{Type: "mention", Path: "plugin://demo"},
		{Type: "text", Text: envelope},
	}
	replies := ParseAsyncQuestionReplyInput(inputs)
	if len(replies) != 1 || replies[0].QuestionItemID != "one" {
		t.Fatalf("parse_input = %#v", replies)
	}
	// Two text items are not a reply message.
	if got := ParseAsyncQuestionReplyInput([]turn.TurnUserInput{{Type: "text", Text: envelope}, {Type: "text", Text: envelope}}); got != nil {
		t.Fatalf("two text items parsed %#v", got)
	}
	// An image alongside the text is not a reply message.
	if got := ParseAsyncQuestionReplyInput([]turn.TurnUserInput{{Type: "text", Text: envelope}, {Type: "image", URL: "https://example.test/a.png"}}); got != nil {
		t.Fatalf("image beside text parsed %#v", got)
	}
	// Skill and mention items alone carry no reply text.
	if got := ParseAsyncQuestionReplyInput([]turn.TurnUserInput{{Type: "skill", Path: "skill://demo"}}); got != nil {
		t.Fatalf("skill-only input parsed %#v", got)
	}
	if !strings.Contains(envelope, "questionItemId") {
		t.Fatal("fixture lost the reply envelope")
	}
}
