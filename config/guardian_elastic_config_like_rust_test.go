package config

import (
	"os"
	"strings"
	"testing"

	codexctx "codex_go/context"
	"codex_go/state"
)

// Mirrors Rust #59444: sync elastic budgeting is opt-in, requires nonempty
// valid sections when enabled, and rejects invalid limits at config load.
func TestGuardianSyncElasticBudgetIsOptInAndValidatedLikeRust(t *testing.T) {
	validSections := `
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 113000
max_section_entry_tokens = 1
`
	invalidCases := map[string]string{
		"missing_sections": "",
		"zero_entry": `
[features.guardianv2.sync_elastic_sections.transcript_tools]
min_total_section_tokens = 1
max_section_entry_tokens = 0
`,
		"maximum_below_minimum": `
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 1
max_section_entry_tokens = 1
max_total_section_tokens = 0
`,
		"minimum_mismatch": `
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 128001
max_section_entry_tokens = 1
`,
		"duplicate_alias": `
[features.guardianv2.sync_elastic_sections.root_messages]
min_total_section_tokens = 1
max_section_entry_tokens = 1
[features.guardianv2.sync_elastic_sections.root_conversation_messages]
min_total_section_tokens = 127999
max_section_entry_tokens = 1
`,
		"unknown_field": `
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 128000
max_section_entry_tokens = 1
unexpected = 1
`,
	}
	for name, sections := range invalidCases {
		for _, enabled := range []bool{false, true} {
			t.Run(name+"/"+map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
				home := t.TempDir()
				body := "[features.guardianv2]\nsync_elastic_budgeting = " + map[bool]string{false: "false", true: "true"}[enabled] + "\n" + sections
				if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				_, err := LoadWithOptions(home, nil)
				if (err != nil) != enabled {
					t.Fatalf("LoadWithOptions() error = %v, enabled = %v", err, enabled)
				}
			})
		}
	}

	home := t.TempDir()
	body := "[features.guardianv2]\nsync_elastic_budgeting = true\n" + validSections
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadWithOptions(valid) error = %v", err)
	}
	policy, err := cfg.GuardianSyncElasticBudget()
	if err != nil || policy == nil {
		t.Fatalf("GuardianSyncElasticBudget() = %#v, %v", policy, err)
	}
	limits := policy.SectionLimits(state.GuardianElasticSectionTrustedSkills)
	if limits.MinTotalSectionTokens != 113000 || limits.MaxSectionEntryTokens != 1 {
		t.Fatalf("trusted skills limits = %#v", limits)
	}
	if cfg.GuardianContextBudgeting() != codexctx.GuardianContextBudgetingElastic {
		t.Fatalf("GuardianContextBudgeting() = %v, want elastic", cfg.GuardianContextBudgeting())
	}
	if (&Config{}).GuardianContextBudgeting() != codexctx.GuardianContextBudgetingFixed {
		t.Fatal("zero config selected elastic budgeting")
	}
	if got := cfg.ValidateGuardianSyncElasticBudget(); got != nil {
		t.Fatalf("ValidateGuardianSyncElasticBudget() error = %v", got)
	}
	if !strings.Contains(body, "sync_elastic_budgeting") {
		t.Fatal("fixture lost its switch")
	}
}
