package state

import (
	"strings"
	"testing"
)

func TestElasticRequestRetainsAnswerBeyondFixedRenderLimit(t *testing.T) {
	answer := strings.Repeat("Proceed only within the approved scope. ", 200)
	retained := guardianElasticVerifiedAnswerContext(t, "large", answer)
	if RenderVerifiedAnswers(retained).Complete {
		t.Fatal("fixture must exceed fixed answer limit")
	}
	for _, presentation := range []ActionPresentation{ActionPresentationSyncFull, ActionPresentationAsync} {
		sections, err := composeGuardianElasticPromptSections(Action{Type: "command", Command: "echo hello", CWD: "/workspace"}, nil, BuildPromptOptions{RetainedContext: retained, Presentation: presentation})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, section := range sections {
			if section.ID == "trusted_user_answers" {
				for _, item := range section.Items {
					if strings.Contains(item.Text, answer) {
						found = true
					}
				}
			}
		}

		parts, _, err := BuildGuardianElasticPromptPartsWithSectionCosts(
			Action{Type: "command", Command: "echo hello", CWD: "/workspace"}, nil,
			BuildPromptOptions{RetainedContext: retained, Presentation: presentation},
			guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{}),
			GuardianElasticRequestBudget{MaxInputTokens: GuardianElasticDefaultMaxInputTokens},
			GuardianBudgetOmissionNotice,
		)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(guardianElasticRenderedText(parts), answer) {
			t.Fatalf("presentation %v lost original answer in final prompt", presentation)
		}
		if !found {
			t.Fatalf("presentation %v lost original answer before allocation", presentation)
		}
	}
	if RenderVerifiedAnswers(retained).Complete {
		t.Fatal("elastic collection changed fixed source rendering")
	}
}
