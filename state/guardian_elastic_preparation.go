package state

import (
	codexctx "codex_go/context"
	"codex_go/utils"
)

// GuardianElasticTruncation records evidence shortened while preparing
// entries, mirroring Rust guardian-context TruncationObservation (#58444).
type GuardianElasticTruncation struct {
	Component     string
	OriginalBytes int
	RetainedBytes int
}

// PrepareGuardianElasticEntries mirrors Rust's
// ComposedContext::prepare_elastic_entries (#58444): supporting text is
// capped once per section entry before optional allocation, so a later grant
// cannot change an entry's representation.
//
// Required and optional text share the same cap. Exact actions, verified
// answers (accepted request_user_input evidence) and opaque media stay whole,
// native sections are never shortened, and request scaffolding ("intro",
// "budget_omission") is not contributed evidence.
// Truncated entries are marked incomplete and their measured cost is cleared
// so allocation charges the retained text.
func PrepareGuardianElasticEntries(context GuardianElasticContext, config GuardianElasticBudgetConfig) (GuardianElasticContext, error) {
	for sectionIndex := range context.Sections {
		section := &context.Sections[sectionIndex]
		if section.Native || section.ID == "intro" || section.ID == "budget_omission" {
			continue
		}
		for itemIndex := range section.Items {
			item := &section.Items[itemIndex]
			group, err := guardianElasticAllocationSection(section.ID, item.Retention)
			if err != nil {
				return GuardianElasticContext{}, err
			}
			maximum := config.SectionLimits(group).MaxSectionEntryTokens
			if item.Exact || item.Protected || item.VerifiedAnswer || item.ImageURL != "" || item.Text == "" {
				// A verified answer is only ever kept or dropped as a whole; cutting
				// it in half would hand the reviewer a question without its answer.
				continue
			}
			framing := guardianElasticContentFramingTokens(1)
			originalTokens := utils.ApproxTokenCount(item.Text)
			if originalTokens+framing <= maximum {
				continue
			}
			budget := maximum - framing
			if budget < 0 {
				budget = 0
			}
			truncated := guardianElasticTruncateTextToFit(item.Text, budget)
			if utils.ApproxTokenCount(truncated) >= originalTokens {
				// Shortening saved nothing, so the original representation stays.
				continue
			}
			context.Truncations = append(context.Truncations, GuardianElasticTruncation{
				Component:     section.ID,
				OriginalBytes: len(item.Text),
				RetainedBytes: len(truncated),
			})
			item.Text = truncated
			item.TokenCost = 0
			item.Incomplete = true
			if item.Source != nil {
				source := *item.Source
				source.Complete = false
				item.Source = &source
			}
		}
	}
	return context, nil
}

// guardianElasticTruncateTextToFit mirrors Rust's truncate_content_to_fit:
// the largest text budget whose approximate token cost fits maxTokens wins,
// both ends and the omission marker are kept, and the minimum marker survives
// a cap too small to hold it. The original text returns when shortening saves
// nothing.
func guardianElasticTruncateTextToFit(text string, maxTokens int) string {
	originalTokens := utils.ApproxTokenCount(text)
	if originalTokens <= maxTokens {
		return text
	}
	upper := int(utils.ApproxTokensFromByteCount(len(text)))
	lower := 0
	selected := lower
	for lower < upper {
		middle := lower + (upper-lower+1)/2
		candidate, _ := codexctx.GuardianTruncateText(text, middle)
		if utils.ApproxTokenCount(candidate) <= maxTokens {
			lower = middle
			selected = middle
		} else {
			upper = middle - 1
		}
	}
	truncated, _ := codexctx.GuardianTruncateText(text, selected)
	if utils.ApproxTokenCount(truncated) >= originalTokens {
		return text
	}
	return truncated
}
