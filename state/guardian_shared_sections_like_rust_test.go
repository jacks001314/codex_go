package state

import (
	"strings"
	"testing"

	"codex_go/retainedctx"
)

// Rust #42076 unifies Guardian context collection so the synchronous reviewer
// and asynchronous scorer receive the same root-conversation, trusted-answer,
// and conversation-transcript sections. Source roles stay line-labeled, and a
// verified answer is delivered exactly once as user-role evidence.
func TestGuardianSharedSectionsAppearOnceForBothTargetsLikeRust(t *testing.T) {
	retained := &retainedctx.RetainedContext{}
	retained.Record(retainedctx.RetainedContextEvent{
		Answer: retainedctx.VerifiedAnswer{
			TurnID:    "turn-1",
			CallID:    "call-1",
			Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Proceed?", Answer: "yes"}},
		},
	})
	options := BuildPromptOptions{
		RootUserAuthorization: []RootMessage{
			{Kind: RootMessageUser, Text: "edit only the scratch directory"},
			{Kind: RootMessageAssistant, Text: "I will keep changes scoped"},
		},
		RetainedContext: retained,
	}
	transcript := []GuardianTranscriptEntry{{Author: "user", LineRole: "user", Text: "continue"}}

	for _, presentation := range []ActionPresentation{ActionPresentationSyncFull, ActionPresentationAsync} {
		options.Presentation = presentation
		parts, _, err := BuildPromptPartsWithSectionCosts(
			Action{Type: "command", Command: "rm -rf scratch", CWD: "/repo"},
			transcript,
			options,
		)
		if err != nil {
			t.Fatalf("presentation %d prompt: %v", presentation, err)
		}
		var prompt strings.Builder
		for _, part := range parts {
			if len(part.Native) == 0 {
				prompt.WriteString(part.Text)
			}
		}
		rendered := prompt.String()
		for _, marker := range []string{
			">>> ROOT CONVERSATION START\n",
			">>> ROOT CONVERSATION END\n",
			">>> TRUSTED USER ANSWERS START\n",
			">>> TRUSTED USER ANSWERS END\n",
		} {
			if got := strings.Count(rendered, marker); got != 1 {
				t.Fatalf("presentation %d marker %q count = %d, want 1\n%s", presentation, marker, got, rendered)
			}
		}
		for _, evidence := range []string{
			"user: edit only the scratch directory",
			"assistant: I will keep changes scoped",
			"assistant: Proceed?",
			"user: yes",
			"user: continue",
		} {
			if got := strings.Count(rendered, evidence); got != 1 {
				t.Fatalf("presentation %d evidence %q count = %d, want 1\n%s", presentation, evidence, got, rendered)
			}
		}
		rootAt := strings.Index(rendered, ">>> ROOT CONVERSATION START\n")
		answersAt := strings.Index(rendered, ">>> TRUSTED USER ANSWERS START\n")
		transcriptAt := strings.Index(rendered, "Recent transcript:")
		if rootAt < 0 || answersAt < 0 || transcriptAt < 0 || !(rootAt < answersAt && answersAt < transcriptAt) {
			t.Fatalf("presentation %d section order = root:%d answers:%d transcript:%d", presentation, rootAt, answersAt, transcriptAt)
		}
	}
}
