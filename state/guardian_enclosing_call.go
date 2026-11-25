package state

import (
	"encoding/json"
	"strings"

	"codex_go/utils"
)

// GuardianEnclosingCall is host-verified evidence from exactly one retained
// function/custom call. It describes context, never the action being approved.
type GuardianEnclosingCall struct {
	Tool      string
	Arguments string
	MaxBytes  int
}

func guardianEnclosingCallText(action Action, options BuildPromptOptions) string {
	call := options.EnclosingCall
	if call == nil || action.Type != "mcp_tool_call" || options.Presentation == ActionPresentationAsync {
		return ""
	}
	maximum := call.MaxBytes
	if maximum <= 0 || maximum > MaxToolEntryTokens*4 {
		maximum = MaxToolEntryTokens * 4
	}
	if len(call.Arguments) > maximum {
		return ""
	}
	data, err := json.Marshal(map[string]string{"tool": call.Tool, "arguments": call.Arguments})
	if err != nil {
		return ""
	}
	text := "Enclosing call for this review: " + string(data) + "\n"
	if len(text) > maximum {
		return ""
	}
	return text
}

func insertGuardianEnclosingCall(actionText, text string) string {
	const marker = ">>> APPROVAL REQUEST START\n"
	if text == "" {
		return actionText
	}
	return strings.Replace(actionText, marker, marker+text, 1)
}

// Late admission gives ordinary evidence priority. Enclosing arguments and
// their label survive together or are omitted together, including under a
// planned_action supporting-evidence cap.
func admitGuardianElasticEnclosingCall(c *GuardianElasticContext, action Action, options BuildPromptOptions, config GuardianElasticBudgetConfig, budget GuardianElasticRequestBudget) {
	text := guardianEnclosingCallText(action, options)
	if text == "" {
		return
	}
	limits := config.SectionLimits(GuardianElasticSectionPlannedAction)
	tokens := utils.ApproxTokenCount(text)
	if tokens+guardianElasticContentFramingTokens(1) > limits.MaxSectionEntryTokens {
		return
	}
	supportingTokens, supportingCount := tokens, 1
	for _, section := range c.Sections {
		if section.ID != "planned_action" || section.Native {
			continue
		}
		for _, item := range section.Items {
			if !item.Protected {
				supportingTokens += guardianElasticItemTokens(item)
				supportingCount++
			}
		}
	}
	supportingTokens += guardianElasticContentFramingTokens(supportingCount+1) - guardianElasticContentFramingTokens(1)
	if limits.MaxTotalSectionTokens != nil && supportingTokens > *limits.MaxTotalSectionTokens {
		return
	}
	for i := range c.Sections {
		section := &c.Sections[i]
		if section.ID != "planned_action" || section.Native {
			continue
		}
		for j, item := range section.Items {
			const marker = ">>> APPROVAL REQUEST START\n"
			at := strings.Index(item.Text, marker)
			if !item.Protected || at < 0 {
				continue
			}
			at += len(marker)
			prefix, suffix := item, item
			prefix.Text, suffix.Text = item.Text[:at], item.Text[at:]
			items := append([]GuardianElasticItem(nil), section.Items[:j]...)
			items = append(items, prefix, GuardianElasticItem{Text: text, Retention: GuardianElasticOptional(GuardianElasticPriorityCommentary), Exact: true}, suffix)
			items = append(items, section.Items[j+1:]...)
			previous := section.Items
			section.Items = items
			if c.EstimatedTokens()+budget.ExistingContextTokens > budget.MaxInputTokens {
				section.Items = previous
			}
			return
		}
	}
}
