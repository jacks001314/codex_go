package state

import (
	"strings"
	"testing"

	"codex_go/retainedctx"
	"codex_go/utils"
)

// guardianElasticVerifiedAnswerContext records one accepted request_user_input
// answer through the same retained event the runtime router writes, so the test
// exercises the recorder-to-section path rather than a hand-built item list.
func guardianElasticVerifiedAnswerContext(t *testing.T, callID string, answer string) *retainedctx.RetainedContext {
	t.Helper()
	context := &retainedctx.RetainedContext{}
	context.Record(retainedctx.RetainedContextEvent{
		Answer: retainedctx.VerifiedAnswer{
			TurnID: "turn-1",
			CallID: callID,
			Questions: []retainedctx.VerifiedQuestionAnswer{{
				Question: "Proceed?",
				Answer:   answer,
			}},
		},
	})
	return context
}

// Mirrors Rust #59902 (guardian-context): an accepted request_user_input answer
// is rendered from retained evidence, so the elastic entry cap may not shorten
// it. One answer that fits the render bound but exceeds the 900-token section
// entry cap once the source-order label and content framing are charged stays
// byte-identical instead of being cut in half.
func TestGuardianElasticKeepsVerifiedAnswersWholeLikeRust(t *testing.T) {
	// 3540 answer bytes keep the rendered record inside RenderVerifiedAnswer's
	// 900-token budget while the label pushes the item past the entry cap.
	retained := guardianElasticVerifiedAnswerContext(t, "call-1", strings.Repeat("a", 3540))
	rendered := RenderVerifiedAnswers(retained)
	if len(rendered.Fragments) != 1 || !rendered.Complete {
		t.Fatalf("RenderVerifiedAnswers() = %#v, want one complete record", rendered)
	}
	fragment := rendered.Fragments[0]
	if cost := utils.ApproxTokenCount(fragment) + guardianElasticContentFramingTokens(1); cost <= GuardianElasticDefaultInstructionTokens {
		t.Fatalf("precondition: verified answer item cost = %d, want more than the %d token entry cap",
			cost, GuardianElasticDefaultInstructionTokens)
	}
	config := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{})
	prepared, err := PrepareGuardianElasticEntries(GuardianElasticContext{Sections: []GuardianElasticSectionOutput{{
		ID:    "trusted_user_answers",
		Items: guardianElasticVerifiedAnswerItems(rendered.Fragments),
	}}}, config)
	if err != nil {
		t.Fatalf("PrepareGuardianElasticEntries() error = %v", err)
	}
	items := prepared.Sections[0].Items
	if len(items) != 1 {
		t.Fatalf("trusted_user_answers items = %d, want the single recorded answer", len(items))
	}
	if items[0].Text != fragment {
		t.Fatalf("verified answer was shortened by the entry cap: retained %d of %d bytes",
			len(items[0].Text), len(fragment))
	}
	if items[0].Incomplete || len(prepared.Truncations) != 0 {
		t.Fatalf("verified answer prepared as %#v with truncations %#v, want it whole",
			items[0], prepared.Truncations)
	}

	// End to end through the synchronous elastic prompt: the recorded answer is
	// still delivered verbatim, so both the question and the answer survive.
	parts, _, err := BuildGuardianElasticPromptPartsWithSectionCosts(
		Action{Type: "command", Command: "echo hello", CWD: "/workspace"},
		nil,
		BuildPromptOptions{RetainedContext: retained, Presentation: ActionPresentationSyncFull},
		config,
		GuardianElasticRequestBudget{MaxInputTokens: GuardianElasticDefaultMaxInputTokens},
		GuardianBudgetOmissionNotice,
	)
	if err != nil {
		t.Fatalf("BuildGuardianElasticPromptPartsWithSectionCosts() error = %v", err)
	}
	prompt := guardianElasticRenderedText(parts)
	if !strings.Contains(prompt, fragment) {
		t.Fatalf("elastic prompt did not deliver the whole verified answer (%d bytes)", len(fragment))
	}
	if strings.Contains(prompt, "<truncated") {
		t.Fatal("elastic prompt shortened evidence although only one verified answer was present")
	}
}

// Mirrors Rust #59902: when the budget cannot fund a verified answer the whole
// record is dropped. Across shrinking budgets the elastic prompt must never
// retain half of one: each answer is either delivered byte-identical or absent.
func TestGuardianElasticDropsVerifiedAnswersWholeLikeRust(t *testing.T) {
	context := guardianElasticVerifiedAnswerContext(t, "call-1", "yes")
	context.Record(retainedctx.RetainedContextEvent{
		Answer: retainedctx.VerifiedAnswer{
			TurnID:    "turn-1",
			CallID:    "call-2",
			Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Proceed?", Answer: strings.Repeat("a", 3540)}},
		},
	})
	rendered := RenderVerifiedAnswers(context)
	if len(rendered.Fragments) != 2 || !rendered.Complete {
		t.Fatalf("RenderVerifiedAnswers() = %#v, want two complete records", rendered)
	}
	config := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{})
	keptTogether := false
	droppedWhole := false
	for budget := 1_000; budget <= 2_600; budget += 50 {
		parts, _, err := BuildGuardianElasticPromptPartsWithSectionCosts(
			Action{Type: "command", Command: "echo hello", CWD: "/workspace"},
			nil,
			BuildPromptOptions{RetainedContext: context, Presentation: ActionPresentationSyncFull},
			config,
			GuardianElasticRequestBudget{MaxInputTokens: budget},
			GuardianBudgetOmissionNotice,
		)
		if err != nil {
			// A budget below the section minimum is rejected rather than served
			// with partial evidence.
			continue
		}
		prompt := guardianElasticRenderedText(parts)
		if strings.Contains(prompt, "<truncated") {
			t.Fatalf("budget %d shortened a verified answer instead of dropping it whole", budget)
		}
		kept := 0
		for _, fragment := range rendered.Fragments {
			if strings.Contains(prompt, fragment) {
				kept++
			}
		}
		switch kept {
		case len(rendered.Fragments):
			keptTogether = true
		case 0:
			droppedWhole = true
		default:
			t.Fatalf("budget %d delivered %d of %d verified answers, want all of them or none",
				budget, kept, len(rendered.Fragments))
		}
	}
	if !keptTogether || !droppedWhole {
		t.Fatalf("sweep keptTogether = %v, droppedWhole = %v, want both outcomes observed",
			keptTogether, droppedWhole)
	}
}

func guardianElasticRenderedText(parts []GuardianPromptPart) string {
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(part.Text)
	}
	return builder.String()
}
