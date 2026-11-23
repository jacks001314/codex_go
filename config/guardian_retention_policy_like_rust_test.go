package config

import (
	"os"
	"testing"

	"codex_go/utils"
)

// Rust #62827: the recorder policy is derived from the Sync Elastic
// retained-instruction entry limit, with the default section limit when the
// configuration omits that section, and stays absent in Fixed mode.
func TestGuardianRetentionPolicyDerivesFromRetainedInstructionEntryBudgetLikeRust(t *testing.T) {
	load := func(t *testing.T, body string) *Config {
		t.Helper()
		home := t.TempDir()
		if err := os.WriteFile(ConfigPath(home), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := LoadWithOptions(home, nil)
		if err != nil {
			t.Fatalf("LoadWithOptions() error = %v", err)
		}
		return cfg
	}

	configured := load(t, `[features.guardianv2]
sync_elastic_budgeting = true
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 113000
max_section_entry_tokens = 1
[features.guardianv2.sync_elastic_sections.retained_user_instructions]
min_total_section_tokens = 900
max_section_entry_tokens = 250
`)
	policy := configured.GuardianRetentionPolicy()
	wantBytes := utils.ApproxBytesForTokens(250)
	if policy == nil || policy.AssistantRecordBytes == nil || *policy.AssistantRecordBytes != wantBytes {
		t.Fatalf("GuardianRetentionPolicy() = %#v, want assistant_record_bytes %d", policy, wantBytes)
	}

	defaulted := load(t, `[features.guardianv2]
sync_elastic_budgeting = true
[features.guardianv2.sync_elastic_sections.trusted_skills]
min_total_section_tokens = 113000
max_section_entry_tokens = 1
`)
	policy = defaulted.GuardianRetentionPolicy()
	wantDefault := utils.ApproxBytesForTokens(900)
	if policy == nil || policy.AssistantRecordBytes == nil || *policy.AssistantRecordBytes != wantDefault {
		t.Fatalf("omitted section policy = %#v, want the default retained-instruction entry limit of %d bytes", policy, wantDefault)
	}

	if fixed := load(t, "").GuardianRetentionPolicy(); fixed != nil {
		t.Fatalf("fixed-mode policy = %#v, want nil", fixed)
	}
	if (*Config)(nil).GuardianRetentionPolicy() != nil {
		t.Fatal("nil config resolved a retention policy")
	}
}
