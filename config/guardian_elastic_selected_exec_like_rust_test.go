package config

import (
	"os"
	"strings"
	"testing"

	"codex_go/state"
)

// TestGuardianSyncElasticSelectedExecSelectionLikeRust covers Rust #60914:
// transcript_selected_exec_results requires nonempty nested_tool_names, other
// sections reject them, and the selection reaches the frozen policy.
func TestGuardianSyncElasticSelectedExecSelectionLikeRust(t *testing.T) {
	valid := `
[features.guardianv2.sync_elastic_sections.transcript_selected_exec_results]
min_total_section_tokens = 1000
max_section_entry_tokens = 250
nested_tool_names = ["mcp__echo__search", "search"]
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 112000
max_section_entry_tokens = 1
`
	invalid := map[string]string{
		"missing_names": `
[features.guardianv2.sync_elastic_sections.transcript_selected_exec_results]
min_total_section_tokens = 1000
max_section_entry_tokens = 250
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 112000
max_section_entry_tokens = 1
`,
		"empty_names": `
[features.guardianv2.sync_elastic_sections.transcript_selected_exec_results]
min_total_section_tokens = 1000
max_section_entry_tokens = 250
nested_tool_names = []
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 112000
max_section_entry_tokens = 1
`,
		"blank_name": `
[features.guardianv2.sync_elastic_sections.transcript_selected_exec_results]
min_total_section_tokens = 1000
max_section_entry_tokens = 250
nested_tool_names = ["search", " "]
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 112000
max_section_entry_tokens = 1
`,
		"wrong_section": `
[features.guardianv2.sync_elastic_sections.transcript_tools]
min_total_section_tokens = 1000
max_section_entry_tokens = 250
nested_tool_names = ["search"]
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 112000
max_section_entry_tokens = 1
`,
	}
	for name, sections := range invalid {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			body := "[features.guardianv2]\nsync_elastic_budgeting = true\n" + sections
			if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadWithOptions(home, nil); err == nil {
				t.Fatal("LoadWithOptions() accepted an invalid selection")
			}
		})
	}

	home := t.TempDir()
	body := "[features.guardianv2]\nsync_elastic_budgeting = true\n" + valid
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
	if got, want := strings.Join(policy.SelectedExecToolNames, ","), "mcp__echo__search,search"; got != want {
		t.Fatalf("selected exec tool names = %q, want %q", got, want)
	}
	limits := policy.SectionLimits(state.GuardianElasticSectionTranscriptSelectedExecResults)
	if limits.MinTotalSectionTokens != 1000 || limits.MaxSectionEntryTokens != 250 {
		t.Fatalf("selected exec limits = %#v", limits)
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// TestGuardianSelectedExecSectionReservesNothingByDefaultLikeRust pins Rust
// #60914's default: an omitted section contributes no minimum reservation.
func TestGuardianSelectedExecSectionReservesNothingByDefaultLikeRust(t *testing.T) {
	policy := state.GuardianElasticBudgetConfig{}
	limits := policy.SectionLimits(state.GuardianElasticSectionTranscriptSelectedExecResults)
	if limits.MinTotalSectionTokens != 0 || limits.MaxSectionEntryTokens != 0 {
		t.Fatalf("default selected exec limits = %#v", limits)
	}
	// Omitted groups still reserve one capped entry, so the zero-value policy
	// has a non-zero total; the new section must not add to it.
	total := policy.TotalMinimumTokens()
	for _, section := range []state.GuardianElasticSection{
		state.GuardianElasticSectionTranscriptMessages,
		state.GuardianElasticSectionTranscriptTools,
		state.GuardianElasticSectionRootConversationMessages,
		state.GuardianElasticSectionRetainedUserInstructions,
		state.GuardianElasticSectionTrustedUserAnswers,
		state.GuardianElasticSectionSenderUserMessages,
		state.GuardianElasticSectionPlannedAction,
		state.GuardianElasticSectionPreviousReviews,
		state.GuardianElasticSectionTrustedTools,
		state.GuardianElasticSectionTrustedSkills,
		state.GuardianElasticSectionTranscriptImages,
		state.GuardianElasticSectionNodeReplEvidence,
		state.GuardianElasticSectionPermissionContext,
	} {
		total -= policy.SectionLimits(section).MinTotalSectionTokens
	}
	if total != 0 {
		t.Fatalf("selected exec section contributed %d reserved tokens", total)
	}
}
