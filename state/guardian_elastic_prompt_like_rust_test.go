package state

import (
	"strings"
	"testing"
)

// TestGuardianElasticPromptHonorsSectionLimitsLikeRust covers Rust #59444's
// synchronous path: the same validated section policy caps one entry and the
// section allocation, adds the budget notice, and keeps the exact action.
func TestGuardianElasticPromptHonorsSectionLimitsLikeRust(t *testing.T) {
	limits, err := NewGuardianElasticSectionLimits(120, 80)
	if err != nil {
		t.Fatal(err)
	}
	limits, err = limits.WithMaxTotalSectionTokens(120)
	if err != nil {
		t.Fatal(err)
	}
	config := guardianElasticCompleteMinimumBudget(t, GuardianElasticBudgetConfig{Sections: map[GuardianElasticSection]GuardianElasticSectionLimits{
		GuardianElasticSectionTranscriptTools: limits,
	}})
	transcript := make([]GuardianTranscriptEntry, 0, 40)
	for index := 0; index < 39; index++ {
		transcript = append(transcript, GuardianTranscriptEntry{
			Author: "tool", LineRole: "tool shell result", Optional: true,
			Text: "tool evidence",
		})
	}
	transcript = append(transcript, GuardianTranscriptEntry{
		Author: "tool", LineRole: "tool shell result", Optional: true,
		Text: strings.Repeat("oversized tool evidence ", 200),
	})
	parts, costs, err := BuildGuardianElasticPromptPartsWithSectionCosts(
		Action{Type: "command", Command: "echo hello", CWD: "/workspace"},
		transcript,
		BuildPromptOptions{TranscriptFormat: GuardianTranscriptFormatLine},
		config,
		GuardianElasticRequestBudget{MaxInputTokens: 3_000},
		GuardianBudgetOmissionNotice,
	)
	if err != nil {
		t.Fatalf("BuildGuardianElasticPromptPartsWithSectionCosts() error = %v", err)
	}
	var rendered strings.Builder
	for _, part := range parts {
		rendered.WriteString(part.Text)
	}
	text := rendered.String()
	if !strings.Contains(text, "echo hello") {
		t.Fatalf("elastic prompt lost the exact action:\n%s", text)
	}
	if !strings.Contains(text, "guardian_context_omission") {
		t.Fatalf("elastic prompt lost the omission notice:\n%s", text)
	}
	if !strings.Contains(text, "<truncated") {
		t.Fatalf("elastic prompt did not apply the entry cap:\n%s", text)
	}
	if count := strings.Count(text, "tool evidence"); count >= len(transcript) {
		t.Fatalf("elastic prompt kept %d tool entries, want a section-bounded prefix", count)
	}
	var transcriptCost *GuardianPromptSectionCost
	for index := range costs {
		if costs[index].Section == "conversation_transcript" {
			transcriptCost = &costs[index]
		}
	}
	if transcriptCost == nil || transcriptCost.TextBytes == 0 {
		t.Fatalf("section costs = %#v, want transcript accounting", costs)
	}
}
