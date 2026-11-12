package state

import (
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/context"
)

// BuildGuardianElasticPromptPartsWithSectionCosts composes the synchronous
// Guardian prompt as allocation-aware sections. Each section keeps its item
// boundaries through entry caps and whole-entry selection, then renders in the
// same order and format as BuildPromptPartsWithSectionCosts.
func BuildGuardianElasticPromptPartsWithSectionCosts(
	action Action,
	transcript []GuardianTranscriptEntry,
	options BuildPromptOptions,
	config GuardianElasticBudgetConfig,
	budget GuardianElasticRequestBudget,
	omissionNotice string,
) ([]GuardianPromptPart, []GuardianPromptSectionCost, error) {
	if err := action.Validate(); err != nil {
		return nil, nil, err
	}
	sections, err := composeGuardianElasticPromptSections(action, transcript, options)
	if err != nil {
		return nil, nil, err
	}
	contextValue := GuardianElasticContext{Sections: sections}
	prepared, err := PrepareGuardianElasticEntries(contextValue, config)
	if err != nil {
		return nil, nil, err
	}
	admitted, err := EnforceGuardianElasticBudgetWithDedup(
		prepared,
		config,
		budget,
		omissionNotice,
		func(contextValue *GuardianElasticContext, selected map[[2]int]bool) (bool, error) {
			return rededuplicateGuardianElasticRetained(contextValue, selected, options)
		},
	)
	if err != nil {
		return nil, nil, err
	}
	return renderGuardianElasticPromptParts(admitted)
}

func composeGuardianElasticPromptSections(
	action Action,
	transcript []GuardianTranscriptEntry,
	options BuildPromptOptions,
) ([]GuardianElasticSectionOutput, error) {
	var sections []GuardianElasticSectionOutput
	add := func(id string, items []GuardianElasticItem, native bool) {
		if len(items) == 0 {
			return
		}
		sections = append(sections, GuardianElasticSectionOutput{ID: id, Items: items, Native: native})
	}
	if len(options.RootUserAuthorization) > 0 {
		messages := make([]RootMessage, 0, len(options.RootUserAuthorization))
		for _, message := range options.RootUserAuthorization {
			if strings.TrimSpace(message.Text) != "" {
				messages = append(messages, message)
			}
		}
		items := RootConversationSectionItems(messages)
		add("root_conversation", guardianElasticTextItems(items, GuardianElasticRequired(), false), false)
	}
	if items := SenderUserMessagesSectionItems(options.RetainedContext); len(items) > 0 {
		add("sender_user_messages", guardianElasticTextItems(items, GuardianElasticRequired(), true), false)
	}
	retained := RenderRetainedInstructionSectionsForPresentation(options.RetainedContext, options.Presentation)
	retained = ApplyRetainedOmissionDeduplication(retained, options.PreviousRetainedOmissions, HasLegacyRetainedOrder(options.RetainedContext))
	add("retained_user_instructions", guardianElasticRetainedItems(retained.Instructions), false)
	if options.Presentation != ActionPresentationAsync {
		add("retained_assistant_context", guardianElasticTextItems(retained.AssistantContext, GuardianElasticOptional(GuardianElasticPriorityCommentary), true), false)
	}
	if items := TrustedUserAnswersSectionItems(RenderVerifiedAnswers(options.RetainedContext).Fragments); len(items) > 0 {
		add("trusted_user_answers", guardianElasticVerifiedAnswerItems(items), false)
	}

	transcriptText := []GuardianElasticItem{{Text: "Recent transcript:\n", Retention: GuardianElasticRequired(), Protected: true}}
	flushTranscript := func() {
		if len(transcriptText) > 0 {
			add("conversation_transcript", transcriptText, false)
			transcriptText = nil
		}
	}
	for _, entry := range transcript {
		if len(entry.Native) > 0 {
			flushTranscript()
			add("conversation_transcript", guardianElasticNativeItems(entry.Native, entry.Retention()), true)
			continue
		}
		line := strings.TrimSpace(entry.Render(options.TranscriptFormat))
		if line == "" {
			continue
		}
		transcriptText = append(transcriptText, GuardianElasticItem{
			Text:      "- " + line + "\n",
			Retention: entry.Retention(),
			Source:    entry.RetainedSource,
		})
	}
	if options.TranscriptOmissionNote != "" {
		transcriptText = append(transcriptText, GuardianElasticItem{
			Text: options.TranscriptOmissionNote + "\n", Retention: GuardianElasticRequired(), Protected: true,
		})
	}
	flushTranscript()

	if options.NodeReplEvidence != nil {
		if rendered := context.Render(options.NodeReplEvidence); rendered != nil && strings.TrimSpace(rendered.Content) != "" {
			add("node_repl_evidence", []GuardianElasticItem{{
				Text: rendered.Content, Retention: GuardianElasticRequired(), Protected: true,
			}}, false)
		}
	}
	if items := PermissionContextSectionItems(options.PermissionContext); len(items) > 0 {
		add("permissions", guardianElasticTextItems(items, GuardianElasticRequired(), true), false)
	}
	if options.Presentation == ActionPresentationAsync {
		if len(retained.AssistantContext) > 0 {
			add("retained_assistant_context", guardianElasticTextItems(retained.AssistantContext, GuardianElasticOptional(GuardianElasticPriorityCommentary), true), false)
		}
	}
	actionPrompt, err := joinPlannedAction(action, options.Presentation)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(actionPrompt) != "" {
		add("planned_action", []GuardianElasticItem{{Text: actionPrompt, Retention: GuardianElasticRequired(), Exact: true, Protected: true}}, false)
	}
	if descriptions := renderGuardianToolDescriptions(action); descriptions != "" {
		add("planned_action", []GuardianElasticItem{{Text: descriptions, Retention: GuardianElasticOptional(GuardianElasticPriorityCommentary)}}, false)
	}
	if strings.TrimSpace(action.Type) == ActionNativeToolCall {
		if _, _, ok := GuardianNativeFunctionCall(action.NativeCall); !ok {
			return nil, fmt.Errorf("guardian native tool call review requires a function call")
		}
		if GuardianNativeFunctionCallTokens(action.NativeCall) > GuardianNativeToolCallMaxTokens {
			return nil, fmt.Errorf("Guardian native tool call evidence exceeds the %d-token review limit", GuardianNativeToolCallMaxTokens)
		}
		add("planned_action", guardianElasticNativeItems(action.NativeCall, GuardianElasticRequired()), true)
	}
	return sections, nil
}

// GuardianElasticPriorityCommentary is the optional non-tool transcript group.
const GuardianElasticPriorityCommentary = "commentary"

func (e GuardianTranscriptEntry) Retention() GuardianElasticRetention {
	if !e.Optional {
		return GuardianElasticRequired()
	}
	if e.SelectedExecResult {
		// Rust #60914: the host reserved this exec output's evidence separately
		// from ordinary tool output.
		return GuardianElasticOptional(GuardianElasticPrioritySelectedExecResult)
	}
	role := strings.ToLower(strings.TrimSpace(e.LineRole + " " + e.Label + " " + e.Author))
	if strings.Contains(role, "tool") {
		return GuardianElasticOptional(GuardianElasticPriorityTool)
	}
	return GuardianElasticOptional(GuardianElasticPriorityCommentary)
}

func guardianElasticTextItems(items []string, retention GuardianElasticRetention, protectFraming bool) []GuardianElasticItem {
	out := make([]GuardianElasticItem, 0, len(items))
	for _, item := range items {
		protected := protectFraming && guardianElasticPromptFraming(item)
		out = append(out, GuardianElasticItem{Text: item, Retention: retention, Protected: protected})
	}
	return out
}

// guardianElasticVerifiedAnswerItems marks the trusted-answer section as
// verified evidence: every item, including the section framing, is kept or
// dropped whole because the section is rendered from accepted
// request_user_input answers (#59902).
func guardianElasticVerifiedAnswerItems(items []string) []GuardianElasticItem {
	out := make([]GuardianElasticItem, 0, len(items))
	for _, item := range items {
		out = append(out, GuardianElasticItem{
			Text:           item,
			Retention:      GuardianElasticRequired(),
			VerifiedAnswer: true,
		})
	}
	return out
}

func guardianElasticRetainedItems(items []string) []GuardianElasticItem {
	out := make([]GuardianElasticItem, 0, len(items))
	for _, item := range items {
		retention := GuardianElasticRequired()
		if strings.HasPrefix(strings.TrimSpace(item), "assistant:") {
			retention = GuardianElasticOptional(GuardianElasticPriorityCommentary)
		}
		out = append(out, GuardianElasticItem{
			Text: item, Retention: retention, Protected: guardianElasticPromptFraming(item),
		})
	}
	return out
}

func guardianElasticPromptFraming(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, ">>>") ||
		strings.HasPrefix(trimmed, "Host notice:") ||
		strings.HasPrefix(trimmed, "Host:")
}

func guardianElasticNativeItems(native json.RawMessage, retention GuardianElasticRetention) []GuardianElasticItem {
	call := append([]byte(nil), native...)
	items := []GuardianElasticItem{{Native: call, Retention: retention}}
	if output, ok := GuardianNativeFunctionCallEvidenceOutput(json.RawMessage(call)); ok {
		items = append(items, GuardianElasticItem{Native: append([]byte(nil), output...), Retention: retention})
	}
	return items
}

func rededuplicateGuardianElasticRetained(
	contextValue *GuardianElasticContext,
	selected map[[2]int]bool,
	options BuildPromptOptions,
) (bool, error) {
	if contextValue == nil || options.RetainedContext == nil {
		return false, nil
	}
	sources := contextValue.RequiredTranscriptSources(selected)
	fragments := RenderRetainedInstructions(options.RetainedContext)
	remaining := RemoveDeliveredRetainedInstructions(fragments, sources, nil)
	var updated RetainedInstructionSections
	if options.Presentation == ActionPresentationAsync {
		updated = retainedInstructionSectionPair(remaining, HasLegacyRetainedOrder(options.RetainedContext))
	} else {
		updated = RetainedInstructionSections{Instructions: retainedInstructionsSectionItems(remaining, HasLegacyRetainedOrder(options.RetainedContext))}
	}
	updated = ApplyRetainedOmissionDeduplication(updated, options.PreviousRetainedOmissions, HasLegacyRetainedOrder(options.RetainedContext))
	oldInstructions := guardianElasticPromptItemsByID(*contextValue, "retained_user_instructions")
	newInstructions := guardianElasticRetainedItems(updated.Instructions)
	oldAssistant := guardianElasticPromptItemsByID(*contextValue, "retained_assistant_context")
	newAssistant := guardianElasticTextItems(updated.AssistantContext, GuardianElasticOptional(GuardianElasticPriorityCommentary), true)
	changed := !guardianElasticPromptItemTextEqual(oldInstructions, newInstructions) ||
		!guardianElasticPromptItemTextEqual(oldAssistant, newAssistant)
	if !changed {
		return false, nil
	}
	guardianElasticReplacePromptItems(contextValue, "retained_user_instructions", newInstructions)
	guardianElasticReplacePromptItems(contextValue, "retained_assistant_context", newAssistant)
	truncations := contextValue.Truncations[:0]
	for _, observation := range contextValue.Truncations {
		if observation.Component != "retained_user_instructions" && observation.Component != "retained_assistant_context" {
			truncations = append(truncations, observation)
		}
	}
	contextValue.Truncations = truncations
	return true, nil
}

func guardianElasticPromptItemsByID(contextValue GuardianElasticContext, id string) []GuardianElasticItem {
	var items []GuardianElasticItem
	for _, section := range contextValue.Sections {
		if section.ID == id && !section.Native {
			items = append(items, section.Items...)
		}
	}
	return items
}

func guardianElasticReplacePromptItems(contextValue *GuardianElasticContext, id string, items []GuardianElasticItem) {
	if contextValue == nil {
		return
	}
	replaced := false
	sections := make([]GuardianElasticSectionOutput, 0, len(contextValue.Sections)+1)
	for _, section := range contextValue.Sections {
		if section.ID != id || section.Native {
			sections = append(sections, section)
			continue
		}
		if !replaced {
			if len(items) > 0 {
				section.Items = append([]GuardianElasticItem(nil), items...)
				sections = append(sections, section)
			}
			replaced = true
		}
	}
	if !replaced && len(items) > 0 {
		sections = append(sections, GuardianElasticSectionOutput{ID: id, Items: append([]GuardianElasticItem(nil), items...)})
	}
	contextValue.Sections = sections
}

func guardianElasticPromptItemTextEqual(left, right []GuardianElasticItem) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Text != right[index].Text || len(left[index].Native) != len(right[index].Native) {
			return false
		}
	}
	return true
}

func renderGuardianElasticPromptParts(contextValue GuardianElasticContext) ([]GuardianPromptPart, []GuardianPromptSectionCost, error) {
	var pending []guardianPromptSection
	var parts []GuardianPromptPart
	var costs []GuardianPromptSectionCost
	recordCost := func(section string, textBytes int, imageBytes int, imageCount int) {
		if textBytes == 0 && imageBytes == 0 && imageCount == 0 {
			return
		}
		if len(costs) > 0 && costs[len(costs)-1].Section == section {
			costs[len(costs)-1].TextBytes += textBytes
			costs[len(costs)-1].ImageBytes += imageBytes
			costs[len(costs)-1].ImageCount += imageCount
			return
		}
		costs = append(costs, GuardianPromptSectionCost{Section: section, TextBytes: textBytes, ImageBytes: imageBytes, ImageCount: imageCount})
	}
	flush := func() {
		for _, section := range pending {
			trimmed := strings.TrimRight(section.text, "\n")
			if strings.TrimSpace(trimmed) == "" {
				continue
			}
			recordCost(section.id, len(trimmed), 0, 0)
			if len(parts) > 0 && len(parts[len(parts)-1].Native) == 0 {
				trimmed = parts[len(parts)-1].Text + "\n\n" + trimmed
				parts = parts[:len(parts)-1]
			}
			for _, chunk := range SplitGuardianTextParts(trimmed, GuardianMaxTextPartBytes) {
				parts = append(parts, GuardianPromptPart{Text: chunk})
			}
		}
		pending = nil
	}
	for _, section := range contextValue.Sections {
		if section.ID == "intro" {
			continue
		}
		if section.Native {
			flush()
			for _, item := range section.Items {
				if len(item.Native) == 0 {
					continue
				}
				native := append(json.RawMessage(nil), item.Native...)
				parts = append(parts, GuardianPromptPart{Native: native})
				recordCost(section.ID, len(native), 0, 0)
			}
			continue
		}
		var text strings.Builder
		for _, item := range section.Items {
			text.WriteString(item.Text)
		}
		if text.Len() > 0 {
			pending = append(pending, guardianPromptSection{id: section.ID, text: text.String()})
		}
	}
	flush()
	return parts, costs, nil
}
