package config

import (
	"os"
	"strings"
	"testing"

	"codex_go/state"
)

// Mirrors Rust #61345: asynchronous elastic budgeting is independent from the
// synchronous switch, opt-in, and validated only when enabled.
func TestGuardianAsyncElasticBudgetIsOptInAndValidatedLikeRust(t *testing.T) {
	validSections := `
[features.guardianv2.async_elastic_sections.trusted_skills]
min_total_section_tokens = 113000
max_section_entry_tokens = 1
`
	invalidCases := map[string]string{
		"missing_sections": "",
		"zero_entry": `
[features.guardianv2.async_elastic_sections.transcript_tools]
min_total_section_tokens = 1
max_section_entry_tokens = 0
`,
		"unknown_field": `
[features.guardianv2.async_elastic_sections.trusted_skills]
min_total_section_tokens = 128000
max_section_entry_tokens = 1
unexpected = 1
`,
	}
	for name, sections := range invalidCases {
		for _, enabled := range []bool{false, true} {
			t.Run(name+"/"+map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
				home := t.TempDir()
				body := "[features.guardianv2]\nasync_elastic_budgeting = " + map[bool]string{false: "false", true: "true"}[enabled] + "\n" + sections
				if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := LoadWithOptions(home, nil); (err != nil) != enabled {
					t.Fatalf("LoadWithOptions() error = %v, enabled = %v", err, enabled)
				}
			})
		}
	}

	home := t.TempDir()
	body := "[features.guardianv2]\nasync_elastic_budgeting = true\n" + validSections
	if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadWithOptions(valid) error = %v", err)
	}
	policy, err := cfg.GuardianAsyncElasticBudget()
	if err != nil || policy == nil {
		t.Fatalf("GuardianAsyncElasticBudget() = %#v, %v", policy, err)
	}
	limits := policy.SectionLimits(state.GuardianElasticSectionTrustedSkills)
	if limits.MinTotalSectionTokens != 113000 || limits.MaxSectionEntryTokens != 1 {
		t.Fatalf("async trusted skills limits = %#v", limits)
	}
	if sync, err := cfg.GuardianSyncElasticBudget(); err != nil || sync != nil {
		t.Fatalf("sync policy = %#v, %v, want nil (independent switch)", sync, err)
	}

	// Async budgeting rejects the sync-only selected exec section.
	home = t.TempDir()
	selected := `[features.guardianv2]
async_elastic_budgeting = true
[features.guardianv2.async_elastic_sections.transcript_selected_exec_results]
min_total_section_tokens = 1
max_section_entry_tokens = 1
nested_tool_names = ["shell"]
`
	if err := os.WriteFile(ConfigPath(home), []byte(selected), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithOptions(home, nil); err == nil || !strings.Contains(err.Error(), "only supported for sync elastic budgeting") {
		t.Fatalf("LoadWithOptions(selected exec) error = %v", err)
	}
}
