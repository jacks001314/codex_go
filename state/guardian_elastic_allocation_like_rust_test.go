package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestGuardianElasticAllocationSelectsRecentWholeEntriesLikeRust covers Rust
// #58388: each group reserves one entry, then shares leftover capacity and
// returns packing gaps to the least-served group. Entries are never split and
// the newest entry in a group is preferred.
func TestGuardianElasticAllocationSelectsRecentWholeEntriesLikeRust(t *testing.T) {
	const entryTokens = 256
	const allowance = 660
	itemCosts := []int{50, 200, 180}
	groups := []GuardianElasticSection{
		GuardianElasticSectionTranscriptMessages,
		GuardianElasticSectionTranscriptTools,
	}
	limits, err := NewGuardianElasticSectionLimits(entryTokens, entryTokens)
	if err != nil {
		t.Fatalf("NewGuardianElasticSectionLimits() error = %v", err)
	}
	config := GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{}}
	for _, group := range groups {
		config.Sections[group] = limits
	}
	demand := guardianElasticDemand{
		groups:        map[GuardianElasticSection]*guardianElasticGroupDemand{},
		contentCounts: make([]int, len(groups)),
		userContent:   make([]bool, len(groups)),
	}
	for sectionIndex, group := range groups {
		entry := &guardianElasticGroupDemand{}
		for itemIndex, cost := range itemCosts {
			entry.pending = append(entry.pending, guardianElasticCandidate{
				sectionIndex: sectionIndex,
				itemIndex:    itemIndex,
				cost:         cost,
			})
			entry.totalCost += cost
		}
		demand.groups[group] = entry
	}

	selected := allocateGuardianElasticItems(&demand, config, allowance)

	want := map[[2]int]bool{
		{0, 0}: true,
		{0, 1}: true,
		{0, 2}: true,
		{1, 0}: true,
		{1, 2}: true,
	}
	if !reflect.DeepEqual(selected, want) {
		t.Fatalf("selected = %v, want %v", selected, want)
	}
}

const guardianElasticTestToolText = "tool evidence "

func wholeImageTestContext() GuardianElasticContext {
	images := make([]GuardianElasticItem, 0, 3)
	for index := range 3 {
		images = append(images, GuardianElasticItem{
			ImageURL:  fmt.Sprintf("data:image/png;base64,original-%d", index),
			Retention: GuardianElasticOptional("image"),
		})
	}
	tools := make([]GuardianElasticItem, 0, 42)
	for range 40 {
		tools = append(tools, GuardianElasticItem{
			Text:      strings.Repeat(guardianElasticTestToolText, 24),
			TokenCost: 1,
			Retention: GuardianElasticOptional(GuardianElasticPriorityTool),
		})
	}
	tools = append(tools,
		GuardianElasticItem{Text: "exact user instruction", TokenCost: 20, Retention: GuardianElasticRequired()},
		GuardianElasticItem{Text: "manual approval", TokenCost: 10, Retention: GuardianElasticRequired()},
	)
	return GuardianElasticContext{Sections: []GuardianElasticSectionOutput{
		{ID: "transcript_images", Items: images},
		{ID: "conversation_transcript", Items: tools},
	}}
}

// TestGuardianElasticAllocationKeepsWholeImagesWithDefaultsLikeRust covers Rust
// #58388: unconfigured groups still reserve the synchronous entry caps, images
// are kept whole and charged the shared framing once, and required evidence is
// never dropped.
func TestGuardianElasticAllocationKeepsWholeImagesWithDefaultsLikeRust(t *testing.T) {
	const (
		entryTokens   = 128
		historyTokens = 12_000
		limitedTokens = 1_000
		notice        = "Evidence omitted"
	)
	limits, err := NewGuardianElasticSectionLimits(entryTokens, entryTokens)
	if err != nil {
		t.Fatalf("NewGuardianElasticSectionLimits() error = %v", err)
	}
	for _, config := range []GuardianElasticBudgetConfig{
		{},
		{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
			GuardianElasticSectionTranscriptTools: limits,
		}},
	} {
		context := wholeImageTestContext()
		budget := GuardianElasticRequestBudget{
			MaxInputTokens:        context.EstimatedTokens() + historyTokens,
			ExistingContextTokens: historyTokens,
		}
		untouched, err := EnforceGuardianElasticBudget(context, config, budget, notice)
		if err != nil {
			t.Fatalf("EnforceGuardianElasticBudget(untouched) error = %v", err)
		}
		if !reflect.DeepEqual(untouched, context) {
			t.Fatalf("untouched context changed: %#v", untouched)
		}

		expected := wholeImageTestContext()
		expected.Sections[0].Items = expected.Sections[0].Items[1:]
		expected.Truncated = true
		expected.Sections = append(expected.Sections, GuardianElasticSectionOutput{
			ID: "budget_omission",
			Items: []GuardianElasticItem{{
				Text:      notice,
				Retention: GuardianElasticRequired(),
			}},
		})
		budget.MaxInputTokens = expected.EstimatedTokens() + historyTokens
		admitted, err := EnforceGuardianElasticBudget(wholeImageTestContext(), config, budget, notice)
		if err != nil {
			t.Fatalf("EnforceGuardianElasticBudget(admitted) error = %v", err)
		}
		if !reflect.DeepEqual(admitted, expected) {
			t.Fatalf("admitted context = %#v, want %#v", admitted, expected)
		}

		budget.MaxInputTokens = limitedTokens + historyTokens
		limited, err := EnforceGuardianElasticBudget(wholeImageTestContext(), config, budget, notice)
		if err != nil {
			t.Fatalf("EnforceGuardianElasticBudget(limited) error = %v", err)
		}
		if tokens := limited.EstimatedTokens(); tokens > limitedTokens {
			t.Fatalf("limited context = %d tokens, want <= %d", tokens, limitedTokens)
		}
		body, err := json.Marshal(limited)
		if err != nil {
			t.Fatalf("Marshal limited context error = %v", err)
		}
		text := string(body)
		if !strings.Contains(text, "exact user instruction") || !strings.Contains(text, "manual approval") {
			t.Fatalf("limited context dropped required evidence: %s", text)
		}
	}
}

// TestGuardianElasticBudgetConfigDecodesLegacySectionAliasesLikeRust covers the
// pre-#58388 serialized group names kept as aliases.
func TestGuardianElasticBudgetConfigDecodesLegacySectionAliasesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name string
		want GuardianElasticSection
	}{
		{"root_messages", GuardianElasticSectionRootConversationMessages},
		{"retained_instructions", GuardianElasticSectionRetainedUserInstructions},
		{"user_answers", GuardianElasticSectionTrustedUserAnswers},
		{"sender_messages", GuardianElasticSectionSenderUserMessages},
		{"tool_descriptions", GuardianElasticSectionPlannedAction},
		{"planned_action_tool_descriptions", GuardianElasticSectionPlannedAction},
		{"images", GuardianElasticSectionTranscriptImages},
		{"repl_evidence", GuardianElasticSectionNodeReplEvidence},
	} {
		raw := fmt.Sprintf(`{"sections":{"%s":{"min_total_section_tokens":10,"max_section_entry_tokens":5}}}`, tc.name)
		var config GuardianElasticBudgetConfig
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatalf("Unmarshal(%s) error = %v", tc.name, err)
		}
		if len(config.Sections) != 1 {
			t.Fatalf("Unmarshal(%s) sections = %v", tc.name, config.Sections)
		}
		limits, ok := config.Sections[tc.want]
		if !ok {
			t.Fatalf("Unmarshal(%s) = %v, want key %q", tc.name, config.Sections, tc.want)
		}
		if limits.MinTotalSectionTokens != 10 || limits.MaxSectionEntryTokens != 5 {
			t.Fatalf("Unmarshal(%s) limits = %#v", tc.name, limits)
		}
	}

	var config GuardianElasticBudgetConfig
	if err := json.Unmarshal([]byte(`{"sections":{"images":{"min_total_section_tokens":10,"max_section_entry_tokens":5}}}`), &config); err != nil {
		t.Fatalf("Unmarshal alias error = %v", err)
	}
	roundTrip, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	want := `{"sections":{"transcript_images":{"min_total_section_tokens":10,"max_section_entry_tokens":5}}}`
	if string(roundTrip) != want {
		t.Fatalf("round trip = %s, want %s", roundTrip, want)
	}
}

// TestGuardianElasticSectionLimitsDefaultToSynchronousCapsLikeRust covers Rust
// ElasticBudgetConfig::section_limits: configured groups win, transcript groups
// fall back to their synchronous entry caps, and every other group reserves one
// default instruction entry.
func TestGuardianElasticSectionLimitsDefaultToSynchronousCapsLikeRust(t *testing.T) {
	entries := SynchronousContextProfile().Transcript.EntryLimits
	var config GuardianElasticBudgetConfig
	for _, tc := range []struct {
		section GuardianElasticSection
		want    int
	}{
		{GuardianElasticSectionTranscriptMessages, entries.MessageTokens},
		{GuardianElasticSectionTranscriptTools, entries.ToolTokens},
		{GuardianElasticSectionRootConversationMessages, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionRetainedUserInstructions, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionTrustedUserAnswers, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionSenderUserMessages, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionPlannedAction, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionPreviousReviews, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionTrustedTools, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionTrustedSkills, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionTranscriptImages, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionNodeReplEvidence, GuardianElasticDefaultInstructionTokens},
		{GuardianElasticSectionPermissionContext, GuardianElasticDefaultInstructionTokens},
	} {
		limits := config.SectionLimits(tc.section)
		if limits.MinTotalSectionTokens != tc.want || limits.MaxSectionEntryTokens != tc.want {
			t.Fatalf("SectionLimits(%q) = %#v, want %d", tc.section, limits, tc.want)
		}
	}
	if GuardianElasticDefaultInstructionTokens != 900 {
		t.Fatalf("GuardianElasticDefaultInstructionTokens = %d, want 900", GuardianElasticDefaultInstructionTokens)
	}
	configured, err := NewGuardianElasticSectionLimits(300, 120)
	if err != nil {
		t.Fatalf("NewGuardianElasticSectionLimits() error = %v", err)
	}
	config.Sections = map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionTranscriptTools: configured,
	}
	if got := config.SectionLimits(GuardianElasticSectionTranscriptTools); got != configured {
		t.Fatalf("configured SectionLimits = %#v, want %#v", got, configured)
	}
}

// TestGuardianElasticEvidenceOverBudgetReturnsLimitError covers Rust
// SectionError::EvidenceLimitExceeded for required evidence that cannot fit.
func TestGuardianElasticEvidenceOverBudgetReturnsLimitError(t *testing.T) {
	context := GuardianElasticContext{Sections: []GuardianElasticSectionOutput{{
		ID: "planned_action",
		Items: []GuardianElasticItem{{
			Text:      "action",
			Retention: GuardianElasticRequired(),
		}},
	}}}
	budget := GuardianElasticRequestBudget{MaxInputTokens: 5}

	_, err := EnforceGuardianElasticBudget(context, GuardianElasticBudgetConfig{}, budget, "omitted")

	if !errors.Is(err, ErrGuardianElasticEvidenceLimit) {
		t.Fatalf("error = %v, want ErrGuardianElasticEvidenceLimit", err)
	}
	var budgetErr GuardianElasticBudgetError
	if !errors.As(err, &budgetErr) || budgetErr.Section != "request_budget" {
		t.Fatalf("error = %v, want request_budget budget error", err)
	}
}
