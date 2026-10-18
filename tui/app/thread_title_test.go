package app

import (
	"strings"
	"testing"
)

func TestRecentThreadTitleConversationSelectsLatestSubstantiveLikeRust(t *testing.T) {
	messages := []ThreadTitleMessage{
		{Role: "user", Text: "old request"},
		{Role: "assistant", Text: "old answer"},
		{Role: "assistant", Text: "working on it", Phase: "commentary"},
		{Role: "user", Text: "Fix <login> & timeout"},
		{Role: "assistant", Text: "Done: fixed the timeout."},
	}
	conversation, ok := RecentThreadTitleConversation(messages)
	if !ok {
		t.Fatal("conversation was empty")
	}
	if strings.Contains(conversation, "working on it") {
		t.Fatalf("commentary leaked into the title conversation: %q", conversation)
	}
	if !strings.Contains(conversation, "<message role=\"user\">Fix &lt;login&gt; &amp; timeout</message>") {
		t.Fatalf("user message was not escaped: %q", conversation)
	}
	if !strings.Contains(conversation, "<message role=\"assistant\">Done: fixed the timeout.</message>") {
		t.Fatalf("assistant message missing: %q", conversation)
	}
	userIndex := strings.Index(conversation, "Fix &lt;login&gt;")
	assistantIndex := strings.Index(conversation, "Done: fixed")
	if userIndex < 0 || assistantIndex < userIndex {
		t.Fatalf("messages are not chronological: %q", conversation)
	}
}

func TestRecentThreadTitleConversationKeepsLatestEightLikeRust(t *testing.T) {
	messages := make([]ThreadTitleMessage, 0, 12)
	for index := 0; index < 12; index++ {
		messages = append(messages, ThreadTitleMessage{Role: "user", Text: "message " + string(rune('a'+index))})
	}
	conversation, ok := RecentThreadTitleConversation(messages)
	if !ok {
		t.Fatal("conversation was empty")
	}
	if strings.Contains(conversation, "message d") {
		t.Fatalf("older message was retained: %q", conversation)
	}
	if !strings.Contains(conversation, "message e") || !strings.Contains(conversation, "message l") {
		t.Fatalf("latest eight messages missing: %q", conversation)
	}
}

func TestRecentConversationThreadTitlePromptBoundsBytesLikeRust(t *testing.T) {
	conversation := strings.Repeat("🚀", 500)
	prompt := RecentConversationThreadTitlePrompt(conversation)
	if len(prompt) > ThreadTitlePromptMaxBytes {
		t.Fatalf("prompt is %d bytes, want <= %d", len(prompt), ThreadTitlePromptMaxBytes)
	}
	if !strings.Contains(prompt, "Prioritize the current task") {
		t.Fatalf("prompt lost its instructions: %q", prompt)
	}
	if strings.ToValidUTF8(prompt, "") != prompt {
		t.Fatal("prompt truncated through a UTF-8 sequence")
	}
}

func TestThreadTitlePromptBoundsUserMessageLikeRust(t *testing.T) {
	prompt := ThreadTitlePrompt(strings.Repeat("界", 2000))
	if len(prompt) > ThreadTitlePromptMaxBytes {
		t.Fatalf("prompt is %d bytes, want <= %d", len(prompt), ThreadTitlePromptMaxBytes)
	}
	if strings.ToValidUTF8(prompt, "") != prompt {
		t.Fatal("prompt truncated through a UTF-8 sequence")
	}
}

func TestThreadTitleOutputSchemaMatchesRust(t *testing.T) {
	schema := ThreadTitleOutputSchema()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("schema = %#v", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	title, ok := properties["title"].(map[string]any)
	if !ok || title["maxLength"] != ThreadTitleMaxChars || title["minLength"] != 1 {
		t.Fatalf("title schema = %#v", properties["title"])
	}
}
