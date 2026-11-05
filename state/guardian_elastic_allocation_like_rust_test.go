package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"codex_go/retainedctx"
)

func guardianElasticCandidateSet(pairs ...[2]int) map[[2]int]bool {
	set := make(map[[2]int]bool, len(pairs))
	for _, pair := range pairs {
		set[pair] = true
	}
	return set
}

// TestGuardianElasticAllocationSelectsRecentWholeEntriesLikeRust covers Rust
// #59401: minimums stay funded, only headroom is shared, Required records
// precede Optional ones, section maxima apply, and duplicate proof pins survive
// refill.
func TestGuardianElasticAllocationSelectsRecentWholeEntriesLikeRust(t *testing.T) {
	const (
		entryTokens         = 256
		itemCount           = 3
		allowance           = 660
		newest              = 2
		maximum             = 300
		requiredCost        = 200
		growingRequiredCost = 150
		fixedMinimum        = 100
		noHeadroomAllowance = fixedMinimum * 2
		secondMinimum       = entryTokens * 2
		granularCount       = 9
		granularMaximum     = 3
		granularFirstCount  = 2
		granularSecondCount = 3
		granularAllowance   = granularFirstCount + granularSecondCount
	)
	groups := []GuardianElasticSection{
		GuardianElasticSectionTranscriptMessages,
		GuardianElasticSectionTranscriptTools,
	}
	itemCosts := []int{50, 200, 180}
	for _, tc := range []struct {
		requiredCost int
		maximum      *int
		fullGroup    *int
		secondMin    int
	}{
		{requiredCost: 0, maximum: nil, fullGroup: intPointer(0), secondMin: entryTokens},
		{requiredCost: 0, maximum: intPointer(maximum), fullGroup: intPointer(1), secondMin: entryTokens},
		{requiredCost: requiredCost, maximum: nil, fullGroup: intPointer(1), secondMin: entryTokens},
		{requiredCost: 0, maximum: nil, fullGroup: nil, secondMin: secondMinimum},
		{requiredCost: growingRequiredCost, maximum: nil, fullGroup: intPointer(0), secondMin: fixedMinimum},
		{requiredCost: 0, maximum: intPointer(granularMaximum), fullGroup: intPointer(0), secondMin: 1},
	} {
		granular := tc.secondMin == 1
		costs := itemCosts
		if granular {
			costs = make([]int, granularCount)
			for index := range costs {
				costs[index] = 1
			}
		}
		minimum := min(tc.secondMin, entryTokens)
		limits, err := NewGuardianElasticSectionLimits(minimum, minimum)
		if err != nil {
			t.Fatal(err)
		}
		secondLimits, err := NewGuardianElasticSectionLimits(tc.secondMin, minimum)
		if err != nil {
			t.Fatal(err)
		}
		config := GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
			groups[1]: secondLimits,
		}}
		if tc.maximum != nil {
			capped, err := limits.WithMaxTotalSectionTokens(*tc.maximum)
			if err != nil {
				t.Fatal(err)
			}
			config.Sections[groups[0]] = capped
		} else {
			config.Sections[groups[0]] = limits
		}
		var pinSets []map[[2]int]bool
		pinSets = append(pinSets, nil)
		if tc.requiredCost == growingRequiredCost {
			pinSets = append(pinSets, guardianElasticCandidateSet([2]int{0, len(costs)}))
		}
		for _, pinned := range pinSets {
			demand := guardianElasticDemand{
				sections:      map[GuardianElasticSection]*guardianElasticSectionDemand{},
				contentCounts: make([]int, len(groups)),
				userContent:   make([]bool, len(groups)),
			}
			for sectionIndex, group := range groups {
				entry := &guardianElasticSectionDemand{}
				if sectionIndex == 0 && tc.requiredCost > 0 {
					entry.requiredItems = append(entry.requiredItems, guardianElasticCandidate{
						sectionIndex: sectionIndex,
						itemIndex:    len(costs),
						cost:         tc.requiredCost,
					})
					entry.totalCost = tc.requiredCost
				}
				for itemIndex, cost := range costs {
					entry.optionalItems = append(entry.optionalItems, guardianElasticCandidate{
						sectionIndex: sectionIndex,
						itemIndex:    itemIndex,
						cost:         cost,
					})
					entry.totalCost += cost
				}
				demand.sections[group] = entry
			}
			selectedAllowance := allowance
			expected := guardianElasticCandidateSet(
				[2]int{0, 0}, [2]int{0, newest}, [2]int{1, 0}, [2]int{1, newest},
			)
			switch {
			case granular:
				selectedAllowance = granularAllowance
				expected = guardianElasticCandidateSet()
				for index := granularCount - granularFirstCount; index < granularCount; index++ {
					expected[[2]int{0, index}] = true
				}
				for index := granularCount - granularSecondCount; index < granularCount; index++ {
					expected[[2]int{1, index}] = true
				}
			case tc.fullGroup != nil:
				expected[[2]int{*tc.fullGroup, 1}] = true
			default:
				selectedAllowance = entryTokens + tc.secondMin
				expected = guardianElasticCandidateSet(
					[2]int{0, 0}, [2]int{0, newest}, [2]int{1, 0}, [2]int{1, 1}, [2]int{1, newest},
				)
			}
			if tc.requiredCost == growingRequiredCost {
				selectedAllowance = noHeadroomAllowance
				expected = guardianElasticCandidateSet([2]int{0, len(costs)}, [2]int{1, 0})
			} else if tc.requiredCost > 0 {
				expected = guardianElasticCandidateSet(
					[2]int{0, len(costs)}, [2]int{0, 0}, [2]int{1, 1}, [2]int{1, newest},
				)
			}
			selected, _, err := allocateGuardianElasticItems(&demand, config, selectedAllowance, pinned)
			if err != nil {
				t.Fatalf("allocateGuardianElasticItems(%+v) error = %v", tc, err)
			}
			if !reflect.DeepEqual(selected, expected) {
				t.Fatalf("selected = %v, want %v for %+v", selected, expected, tc)
			}
		}
	}
}

func intPointer(value int) *int { return &value }

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
			GuardianElasticSectionTranscriptTools:  limits,
			GuardianElasticSectionTranscriptImages: limits,
		}},
	} {
		config = guardianElasticCompleteMinimumBudget(t, config)
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
		expected.Truncations = append(expected.Truncations, GuardianElasticTruncation{Component: "transcript_images"})
		expected.Sections = append(expected.Sections, guardianElasticOmissionSection(notice))
		budget.MaxInputTokens = expected.EstimatedTokens() + historyTokens
		admitted, err := EnforceGuardianElasticBudget(wholeImageTestContext(), config, budget, notice)
		if err != nil {
			t.Fatalf("EnforceGuardianElasticBudget(admitted) error = %v", err)
		}
		if !reflect.DeepEqual(admitted, expected) {
			t.Fatalf("admitted images=%d tools=%d truncated=%v truncations=%#v; expected images=%d tools=%d truncated=%v truncations=%#v; last admitted=%#v last expected=%#v",
				len(admitted.Sections[0].Items), len(admitted.Sections[1].Items), admitted.Truncated, admitted.Truncations,
				len(expected.Sections[0].Items), len(expected.Sections[1].Items), expected.Truncated, expected.Truncations,
				admitted.Sections[len(admitted.Sections)-1], expected.Sections[len(expected.Sections)-1])
		}

		budget.MaxInputTokens = limitedTokens + historyTokens
		limited, err := EnforceGuardianElasticBudget(wholeImageTestContext(), config, budget, notice)
		if config.SectionLimits(GuardianElasticSectionTranscriptTools).MinTotalSectionTokens >= limitedTokens {
			if !errors.Is(err, ErrGuardianElasticEvidenceLimit) {
				t.Fatalf("EnforceGuardianElasticBudget(limited) error = %v, want the funded minimum to exceed the budget", err)
			}
			continue
		}
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

	config := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{})
	_, err := EnforceGuardianElasticBudget(context, config, budget, "omitted")

	if !errors.Is(err, ErrGuardianElasticEvidenceLimit) {
		t.Fatalf("error = %v, want ErrGuardianElasticEvidenceLimit", err)
	}
	var budgetErr GuardianElasticBudgetError
	if !errors.As(err, &budgetErr) || budgetErr.Section != "request_budget" {
		t.Fatalf("error = %v, want request_budget budget error", err)
	}
}

// TestGuardianElasticRequiredRespectsMaximumWhenRequestFitsLikeRust covers
// #59401's section ceiling: a request that fits globally still drops Required
// evidence beyond the section maximum and adds the omission notice.
func TestGuardianElasticRequiredRespectsMaximumWhenRequestFitsLikeRust(t *testing.T) {
	const (
		minimum = 16
		larger  = minimum * 2
	)
	for _, maximum := range []int{minimum, larger} {
		limits, err := NewGuardianElasticSectionLimits(minimum, minimum)
		if err != nil {
			t.Fatal(err)
		}
		limits, err = limits.WithMaxTotalSectionTokens(maximum)
		if err != nil {
			t.Fatal(err)
		}
		config := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
			GuardianElasticSectionTranscriptMessages: limits,
		}})
		context := GuardianElasticContext{Sections: []GuardianElasticSectionOutput{{
			ID: "conversation_transcript",
			Items: []GuardianElasticItem{{
				Text:      "required",
				TokenCost: minimum,
				Retention: GuardianElasticRequired(),
			}},
		}}}
		budget := GuardianElasticRequestBudget{MaxInputTokens: context.EstimatedTokens() + 2_000, ExistingContextTokens: 1_000}
		got, err := EnforceGuardianElasticBudget(context, config, budget, "omitted")
		if err != nil {
			t.Fatalf("EnforceGuardianElasticBudget(max=%d) error = %v", maximum, err)
		}
		kept := got.Sections[0].Items
		if maximum == minimum {
			if len(kept) != 0 || !got.Truncated || got.Sections[len(got.Sections)-1].ID != "budget_omission" {
				t.Fatalf("maximum %d kept %#v, want Required evidence dropped with a notice", maximum, got)
			}
			continue
		}
		if len(kept) != 1 || !reflect.DeepEqual(kept[0], context.Sections[0].Items[0]) {
			t.Fatalf("maximum %d kept %#v, want the Required evidence", maximum, kept)
		}
	}
}

// TestGuardianElasticSelectedProofRefillsLikeRust covers #59401's reallocation
// loop: only a Required transcript entry selected by allocation proves delivery,
// and removing credited instructions releases budget for another pass.
func TestGuardianElasticSelectedProofRefillsLikeRust(t *testing.T) {
	source := &retainedctx.RetainedSource{Complete: true}
	source.ID.MessageID = "user-1"
	context := GuardianElasticContext{Sections: []GuardianElasticSectionOutput{
		{ID: "conversation_transcript", Items: []GuardianElasticItem{{
			Text:      "original instruction",
			TokenCost: 20,
			Retention: GuardianElasticRequired(),
			Source:    source,
		}}},
		{ID: "retained_user_instructions", Items: []GuardianElasticItem{{
			Text:      "duplicate instruction",
			TokenCost: 20,
			Retention: GuardianElasticRequired(),
		}}},
	}}
	limits, err := NewGuardianElasticSectionLimits(32, 32)
	if err != nil {
		t.Fatal(err)
	}
	config := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionTranscriptMessages:       limits,
		GuardianElasticSectionRetainedUserInstructions: limits,
	}})
	calls := 0
	got, err := EnforceGuardianElasticBudgetWithDedup(
		context,
		config,
		GuardianElasticRequestBudget{MaxInputTokens: context.EstimatedTokens()},
		"omitted",
		func(context *GuardianElasticContext, selected map[[2]int]bool) (bool, error) {
			calls++
			sources := context.RequiredTranscriptSources(selected)
			if len(sources) != 1 || sources[0].ID.MessageID != "user-1" {
				t.Fatalf("selected sources = %#v, want the complete transcript proof", sources)
			}
			if len(context.Sections[1].Items) == 0 {
				return false, nil
			}
			context.Sections[1].Items = nil
			return true, nil
		},
	)
	if err != nil {
		t.Fatalf("EnforceGuardianElasticBudgetWithDedup() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("deduplicator calls = %d, want one refill", calls)
	}
	if len(got.Sections[0].Items) != 1 || len(got.Sections[1].Items) != 0 {
		t.Fatalf("deduplicated context = %#v, want transcript proof retained", got)
	}
}
