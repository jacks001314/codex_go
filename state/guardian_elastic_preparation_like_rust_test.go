package state

import (
	"errors"
	"strings"
	"testing"

	"codex_go/retainedctx"
)

func guardianElasticTestLimits(t *testing.T, tokens int) GuardianElasticSectionLimits {
	t.Helper()
	limits, err := NewGuardianElasticSectionLimits(tokens, tokens)
	if err != nil {
		t.Fatalf("NewGuardianElasticSectionLimits(%d) error = %v", tokens, err)
	}
	return limits
}

// Mirrors Rust #58444
// (guardian-context/src/preparation/elastic_tests.rs): the entry cap bounds
// required and optional text before allocation, keeps short and opaque
// entries whole, marks shortened sources incomplete, and records truncation
// sizes.
func TestGuardianElasticEntryCapsBeforeAllocationLikeRust(t *testing.T) {
	const entryTokens = 256
	longRequired := strings.Repeat("required evidence ", 2000)
	source := &retainedctx.RetainedSource{Complete: true}
	longOptional := strings.Repeat("quoted \"text\" detail ", 2000)
	short := GuardianElasticItem{
		Text:      "small result",
		Retention: GuardianElasticOptional(GuardianElasticPriorityTool),
	}
	image := GuardianElasticItem{
		ImageURL:  "data:image/png;base64,original",
		Retention: GuardianElasticOptional(""),
	}
	native := GuardianElasticItem{
		Text:      strings.Repeat("opaque arguments ", 2000),
		Retention: GuardianElasticOptional(GuardianElasticPriorityTool),
	}
	context := GuardianElasticContext{Sections: []GuardianElasticSectionOutput{
		{
			ID: "conversation_transcript",
			Items: []GuardianElasticItem{
				{Text: longRequired, Retention: GuardianElasticRequired(), Source: source},
				short,
				{Text: longOptional, Retention: GuardianElasticOptional(GuardianElasticPriorityTool)},
			},
		},
		{ID: "transcript_images", Items: []GuardianElasticItem{image}},
		{ID: "conversation_transcript", Native: true, Items: []GuardianElasticItem{native}},
	}}
	limits := guardianElasticTestLimits(t, entryTokens)
	config := GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionTranscriptMessages: limits,
		GuardianElasticSectionTranscriptTools:    limits,
	}}
	prepared, err := PrepareGuardianElasticEntries(context, config)
	if err != nil {
		t.Fatalf("PrepareGuardianElasticEntries() error = %v", err)
	}
	items := prepared.Sections[0].Items
	if !items[0].Incomplete || !strings.Contains(items[0].Text, "<truncated") {
		t.Fatalf("required entry = %#v, want a marked truncation", items[0])
	}
	if items[0].Source == nil || items[0].Source.Complete {
		t.Fatalf("required source = %#v, want an incomplete source", items[0].Source)
	}
	if got := guardianElasticItemTokens(items[0]) + guardianElasticContentFramingTokens(1); got > entryTokens {
		t.Fatalf("required entry cost = %d, want <= %d", got, entryTokens)
	}
	if items[1] != short {
		t.Fatalf("short entry = %#v, want it unchanged", items[1])
	}
	if !items[2].Incomplete || !strings.Contains(items[2].Text, "<truncated") {
		t.Fatalf("optional entry = %#v, want a marked truncation", items[2])
	}
	if got := guardianElasticItemTokens(items[2]) + guardianElasticContentFramingTokens(1); got > entryTokens {
		t.Fatalf("optional entry cost = %d, want <= %d", got, entryTokens)
	}
	if got := prepared.Sections[1].Items[0]; got != image {
		t.Fatalf("image entry = %#v, want it whole", got)
	}
	if got := prepared.Sections[2].Items[0]; got != native {
		t.Fatalf("native entry = %#v, want it whole", got)
	}
	if len(prepared.Truncations) != 2 {
		t.Fatalf("truncations = %#v, want two observations", prepared.Truncations)
	}
	for _, observation := range prepared.Truncations {
		if observation.Component != "conversation_transcript" || observation.RetainedBytes >= observation.OriginalBytes {
			t.Fatalf("truncation = %#v", observation)
		}
	}

	// A cap too small for the marker keeps required evidence present.
	tiny := guardianElasticTestLimits(t, 1)
	tinyConfig := GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionTranscriptMessages: tiny,
		GuardianElasticSectionTranscriptTools:    tiny,
	}}
	minimalContext := GuardianElasticContext{Sections: context.Sections[:1]}
	minimal, err := PrepareGuardianElasticEntries(minimalContext, tinyConfig)
	if err != nil {
		t.Fatalf("PrepareGuardianElasticEntries(tiny) error = %v", err)
	}
	tinyConfig = guardianElasticCompleteMinimumBudget(t, tinyConfig)
	minimal, err = EnforceGuardianElasticBudget(minimal, tinyConfig, GuardianElasticRequestBudget{MaxInputTokens: entryTokens}, "Evidence was shortened.")
	if err != nil {
		t.Fatalf("EnforceGuardianElasticBudget(tiny) error = %v", err)
	}
	if len(minimal.Sections[0].Items) == 0 || minimal.Sections[0].Items[0].Retention.Optional {
		t.Fatalf("required evidence removed: %#v", minimal.Sections[0])
	}
}

// Mirrors Rust #58444: the exact planned-action JSON stays whole while
// supporting text is capped, so a budget smaller than the action is
// rejected instead of shortening the reviewed action.
func TestGuardianElasticExactActionStaysWholeLikeRust(t *testing.T) {
	const entryTokens = 128
	exactText := `{"tool":"write_record","arguments":{"description":"` +
		strings.Repeat("exact action payload ", 1000) + `"}}` + "\n"
	supporting := strings.Repeat("supporting context ", 1000)
	context := GuardianElasticContext{Sections: []GuardianElasticSectionOutput{{
		ID: "planned_action",
		Items: []GuardianElasticItem{
			{Text: exactText, Exact: true, Retention: GuardianElasticRequired()},
			{Text: supporting, Retention: GuardianElasticRequired()},
		},
	}}}
	config := GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionPlannedAction: guardianElasticTestLimits(t, entryTokens),
	}}
	prepared, err := PrepareGuardianElasticEntries(context, config)
	if err != nil {
		t.Fatalf("PrepareGuardianElasticEntries() error = %v", err)
	}
	items := prepared.Sections[0].Items
	if items[0] != (GuardianElasticItem{Text: exactText, Exact: true, Retention: GuardianElasticRequired()}) {
		t.Fatalf("exact action = %#v, want it whole", items[0])
	}
	if !items[1].Incomplete || !strings.Contains(items[1].Text, "<truncated") {
		t.Fatalf("supporting text = %#v, want a marked truncation", items[1])
	}
	config = guardianElasticCompleteMinimumBudget(t, config)
	if _, err := EnforceGuardianElasticBudget(prepared, config, GuardianElasticRequestBudget{MaxInputTokens: entryTokens}, "Evidence omitted."); !errors.Is(err, ErrGuardianElasticEvidenceLimit) {
		t.Fatalf("EnforceGuardianElasticBudget(smaller than the action) error = %v, want the evidence limit", err)
	}
	if _, err := EnforceGuardianElasticBudget(prepared, config, GuardianElasticRequestBudget{MaxInputTokens: prepared.EstimatedTokens() + entryTokens}, "Evidence omitted."); err != nil {
		t.Fatalf("EnforceGuardianElasticBudget(exact estimate) error = %v", err)
	}
}
