package config

import (
	"os"
	"testing"

	"codex_go/state"
)

// Mirrors Rust #63368: transcript_audio loads as an optional elastic group for
// both the synchronous reviewer and the asynchronous scorer.
func TestTranscriptAudioElasticBudgetLoadsForBothReviewersLikeRust(t *testing.T) {
	const audioMinimum = 2_000
	const audioEntry = 500
	const audioMaximum = 4_000
	// Reducing transcript_messages by the audio reservation and completing
	// trusted_skills keeps the section minimums at DEFAULT_MAX_INPUT_TOKENS.
	body := "[features.guardianv2]\n" +
		"sync_elastic_budgeting = true\n" +
		"async_elastic_budgeting = true\n" +
		"[features.guardianv2.sync_elastic_sections.transcript_messages]\n" +
		"min_total_section_tokens = 3000\n" +
		"max_section_entry_tokens = 3000\n" +
		"[features.guardianv2.sync_elastic_sections.transcript_audio]\n" +
		"min_total_section_tokens = 2000\n" +
		"max_section_entry_tokens = 500\n" +
		"max_total_section_tokens = 4000\n" +
		"[features.guardianv2.sync_elastic_sections.trusted_skills]\n" +
		"min_total_section_tokens = 113000\n" +
		"max_section_entry_tokens = 1\n" +
		"[features.guardianv2.async_elastic_sections.transcript_messages]\n" +
		"min_total_section_tokens = 3000\n" +
		"max_section_entry_tokens = 3000\n" +
		"[features.guardianv2.async_elastic_sections.transcript_audio]\n" +
		"min_total_section_tokens = 2000\n" +
		"max_section_entry_tokens = 500\n" +
		"max_total_section_tokens = 4000\n" +
		"[features.guardianv2.async_elastic_sections.trusted_skills]\n" +
		"min_total_section_tokens = 113000\n" +
		"max_section_entry_tokens = 1\n"
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadWithOptions() error = %v", err)
	}
	for name, policy := range map[string]func() (*state.GuardianElasticBudgetConfig, error){
		"sync":  cfg.GuardianSyncElasticBudget,
		"async": cfg.GuardianAsyncElasticBudget,
	} {
		budget, err := policy()
		if err != nil || budget == nil {
			t.Fatalf("%s policy = %#v, %v", name, budget, err)
		}
		limits := budget.SectionLimits(state.GuardianElasticSectionTranscriptAudio)
		if limits.MinTotalSectionTokens != audioMinimum || limits.MaxSectionEntryTokens != audioEntry ||
			limits.MaxTotalSectionTokens == nil || *limits.MaxTotalSectionTokens != audioMaximum {
			t.Fatalf("%s transcript_audio limits = %#v", name, limits)
		}
	}
}
