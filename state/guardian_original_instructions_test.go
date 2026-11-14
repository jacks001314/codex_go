package state

import (
	"strings"
	"testing"

	"codex_go/retainedctx"
)

func TestElasticRetainedSectionsKeepLargeOriginalsInBothPresentations(t *testing.T) {
	user := strings.Repeat("Use staging only. ", 600)
	assistant := strings.Repeat("I will verify staging. ", 600)
	context := &retainedctx.RetainedContext{}
	context.RecordUserMessage(retainedctx.RetainedUserMessage{TurnID: "u", MessageID: retainedStringPtr("u"), Text: user, Complete: true}, retainedctx.LocalInputSource(retainedUint64Ptr(0)))
	context.RecordAssistantMessage(retainedctx.RetainedUserMessage{TurnID: "a", MessageID: retainedStringPtr("a"), Text: assistant, Complete: true}, retainedctx.LocalInputSource(retainedUint64Ptr(1)))
	for _, presentation := range []ActionPresentation{ActionPresentationSyncFull, ActionPresentationAsync} {
		fixed := RenderRetainedInstructionSectionsForPresentation(context, presentation)
		if strings.Contains(strings.Join(fixed.Instructions, ""), user) {
			t.Fatal("fixture fits fixed limit")
		}
		sections, err := composeGuardianElasticPromptSections(Action{Type: "command", Command: "echo hello", CWD: "/workspace"}, nil, BuildPromptOptions{RetainedContext: context, Presentation: presentation})
		if err != nil {
			t.Fatal(err)
		}

		prepared := GuardianElasticContext{Sections: sections}
		_, err = rededuplicateGuardianElasticRetained(&prepared, nil, BuildPromptOptions{RetainedContext: context, Presentation: presentation})
		if err != nil {
			t.Fatal(err)
		}

		limits, err := NewGuardianElasticSectionLimits(10000, 4000)
		if err != nil {
			t.Fatal(err)
		}
		budgetConfig := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
			GuardianElasticSectionRetainedUserInstructions: limits,
		}})
		parts, _, err := BuildGuardianElasticPromptPartsWithSectionCosts(
			Action{Type: "command", Command: "echo hello", CWD: "/workspace"}, nil,
			BuildPromptOptions{RetainedContext: context, Presentation: presentation}, budgetConfig,
			GuardianElasticRequestBudget{MaxInputTokens: GuardianElasticDefaultMaxInputTokens}, GuardianBudgetOmissionNotice,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(guardianElasticRenderedText(parts), user) {
			t.Fatalf("presentation %v lost user original in final prompt", presentation)
		}
		if !strings.Contains(guardianElasticRenderedText(parts), assistant) {
			t.Fatalf("presentation %v lost assistant original in final prompt", presentation)
		}
		sections = prepared.Sections
		var text strings.Builder
		for _, section := range sections {
			for _, item := range section.Items {
				text.WriteString(item.Text)
			}
		}
		if !strings.Contains(text.String(), user) || !strings.Contains(text.String(), assistant) {
			t.Fatalf("presentation %v lost original before allocation", presentation)
		}
	}
}
