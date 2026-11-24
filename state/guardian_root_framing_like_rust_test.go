package state

import (
	"strings"
	"testing"
)

// TestGuardianRootConversationElasticFramingLikeRust covers Rust #59003: the
// root-conversation markers and authorization warnings keep their full text
// under an elastic entry cap while the user evidence inside is shortened.
func TestGuardianRootConversationElasticFramingLikeRust(t *testing.T) {
	long := strings.Repeat("root instruction ", 400)
	options := BuildPromptOptions{
		TranscriptFormat: GuardianTranscriptFormatLine,
		RootUserAuthorization: []RootMessage{
			{Kind: RootMessageRetainedContextScope},
			{Kind: RootMessageIncompleteVerifiedAnswers},
			{Kind: RootMessageUser, Text: long},
			{Kind: RootMessageUser, Text: "keep tests green"},
		},
	}
	config := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionRootConversationMessages: guardianElasticTestLimits(t, 24),
	}})
	parts, _, err := BuildGuardianElasticPromptPartsWithSectionCosts(
		Action{Type: "command", Command: "echo hello", CWD: "/workspace"},
		nil,
		options,
		config,
		GuardianElasticRequestBudget{MaxInputTokens: GuardianElasticDefaultMaxInputTokens},
		GuardianBudgetOmissionNotice,
	)
	if err != nil {
		t.Fatalf("BuildGuardianElasticPromptPartsWithSectionCosts() error = %v", err)
	}
	var rendered strings.Builder
	for _, part := range parts {
		rendered.WriteString(part.Text)
	}
	text := rendered.String()
	for _, want := range []string{
		">>> ROOT CONVERSATION START",
		"only user messages can authorize actions",
		"Host notice: some verified user answers are unavailable",
		"Messages with known positions are in recorded order",
		"user: keep tests green",
		">>> ROOT CONVERSATION END",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("elastic prompt missing %q:\n%s", want, text)
		}
	}
	if count := strings.Count(text, "<truncated"); count != 1 {
		t.Fatalf("truncated entries = %d, want only the long user message:\n%s", count, text)
	}
}

// TestGuardianRootConversationFixedPromptKeepsHostNoticesLikeRust keeps the
// rendered host notices that carry an empty Text field; before Rust #59003 the
// prompt filtered them out and lost its authorization warnings.
func TestGuardianRootConversationFixedPromptKeepsHostNoticesLikeRust(t *testing.T) {
	prompt, err := BuildPromptWithOptions(Action{Type: "command", Command: "ls", CWD: "/repo"}, []string{"user: inspect only"}, BuildPromptOptions{
		RootUserAuthorization: []RootMessage{
			{Kind: RootMessageRetainedContextScope},
			{Kind: RootMessageIncompleteRootInstructions},
			{Kind: RootMessageUser, Text: "I approve listing files."},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		">>> ROOT CONVERSATION START",
		"Host notice: some root user instructions are unavailable",
		"Messages with known positions are in recorded order",
		"user: I approve listing files.",
		">>> ROOT CONVERSATION END",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("fixed prompt missing %q:\n%s", want, prompt)
		}
	}
}

// TestGuardianRootConversationProtectionLikeRust pins which root-conversation
// items elastic entry caps must keep whole.
func TestGuardianRootConversationProtectionLikeRust(t *testing.T) {
	items := RootConversationElasticItems([]RootMessage{
		{Kind: RootMessageUser, Text: "approve"},
		{Kind: RootMessageAssistant, Text: "context"},
		{Kind: RootMessageLegacyContextScope},
		{Kind: RootMessageIncompleteAssistantContext},
	})
	if len(items) != 7 {
		t.Fatalf("items = %d, want start, notice, two messages, two notices, end", len(items))
	}
	wantProtected := []bool{true, true, false, false, true, true, true}
	for index, want := range wantProtected {
		if items[index].Protected != want {
			t.Fatalf("item %d protected = %t, want %t (%q)", index, items[index].Protected, want, items[index].Text)
		}
		if items[index].Retention.Optional {
			t.Fatalf("root conversation item %d became optional", index)
		}
	}
	if got := RootConversationElasticItems(nil); got != nil {
		t.Fatalf("empty root conversation items = %#v, want nil", got)
	}
}
