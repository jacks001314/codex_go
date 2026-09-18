package state

import (
	"strings"
	"testing"
)

// TestRenderGuardianPolicyInstructionsMatchesRust mirrors Rust
// prompts::guardian_instructions_tests: the template keeps its layout, every
// placeholder receives the trimmed policy, and the output contract terminates
// the instructions.
func TestRenderGuardianPolicyInstructionsMatchesRust(t *testing.T) {
	for _, testCase := range []struct {
		template string
		policy   string
		want     string
	}{
		{
			template: " Review:\n{{ tenant_policy_config }}\nAgain: {{ tenant_policy_config }}\n \t",
			policy:   " \nTenant policy.\n ",
			want:     " Review:\nTenant policy.\nAgain: Tenant policy.\n\nReview contract.\n",
		},
		{
			template: "Policy: {{ tenant_policy_config }}",
			policy:   "",
			want:     "Policy: \n\nReview contract.\n",
		},
		{
			template: "",
			policy:   "Tenant policy.",
			want:     "\n\nReview contract.\n",
		},
	} {
		if got := RenderGuardianPolicyInstructions(testCase.policy, testCase.template, "Review contract."); got != testCase.want {
			t.Fatalf("RenderGuardianPolicyInstructions() = %q, want %q", got, testCase.want)
		}
	}
}

// TestBundledGuardianTextIsVendored pins the vendored reviewer assets and the
// output contract so a missing or truncated asset fails loudly.
func TestBundledGuardianTextIsVendored(t *testing.T) {
	policy := GuardianPolicy()
	if !strings.Contains(policy, "## Environment Profile") || !strings.Contains(policy, "## Risk Taxonomy and Allow/Deny Rules") {
		t.Fatalf("bundled Guardian policy is not the Rust document:\n%.200s", policy)
	}
	template := GuardianPolicyTemplate()
	if !strings.Contains(template, "You are judging one planned coding-agent action.") || !strings.Contains(template, guardianPolicyPlaceholder) {
		t.Fatalf("bundled Guardian policy template is not the Rust document:\n%.200s", template)
	}
	contract := GuardianOutputContractPrompt()
	if !strings.Contains(contract, "your final message must be strict JSON") || !strings.Contains(contract, `"rationale": string`) {
		t.Fatalf("Guardian output contract = %q", contract)
	}
}
