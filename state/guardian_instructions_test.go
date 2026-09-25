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
			// Rust #47125: the extra-policy slot is empty when no extra policy
			// is configured.
			template: "Tenant: {{ tenant_policy_config }}\nAdditional: {{ extra_policy }}",
			policy:   "Tenant policy.",
			want:     "Tenant: Tenant policy.\nAdditional: \n\nReview contract.\n",
		},
		{
			template: "",
			policy:   "Tenant policy.",
			want:     "\n\nReview contract.\n",
		},
	} {
		if got := RenderGuardianPolicyInstructions(testCase.policy, "", testCase.template, "Review contract."); got != testCase.want {
			t.Fatalf("RenderGuardianPolicyInstructions() = %q, want %q", got, testCase.want)
		}
	}
}

// TestRenderGuardianExtraPolicyMatchesRust mirrors Rust #47125's
// reviewer_extra_policy_substitution_preserves_tenant_policy: every extra
// placeholder receives the trimmed extra policy, and a template without the
// slot ignores it.
func TestRenderGuardianExtraPolicyMatchesRust(t *testing.T) {
	for _, testCase := range []struct {
		template    string
		extraPolicy string
		want        string
	}{
		{
			template:    "Tenant: {{ tenant_policy_config }}\nAdditional: {{ extra_policy }}\nAgain: {{ extra_policy }}",
			extraPolicy: " \nAdditional policy.\n ",
			want:        "Tenant: Tenant policy.\nAdditional: Additional policy.\nAgain: Additional policy.\n\nReview contract.\n",
		},
		{
			template:    "Tenant: {{ tenant_policy_config }}\nAdditional: {{ extra_policy }}",
			extraPolicy: " \n\t",
			want:        "Tenant: Tenant policy.\nAdditional: \n\nReview contract.\n",
		},
		{
			template:    "Tenant: {{ tenant_policy_config }}",
			extraPolicy: "Additional policy.",
			want:        "Tenant: Tenant policy.\n\nReview contract.\n",
		},
	} {
		if got := RenderGuardianPolicyInstructions("Tenant policy.", testCase.extraPolicy, testCase.template, "Review contract."); got != testCase.want {
			t.Fatalf("RenderGuardianPolicyInstructions() = %q, want %q", got, testCase.want)
		}
	}

	// Placeholder-like text inside either supplied policy stays literal.
	got := RenderGuardianPolicyInstructions(
		"Tenant says {{ tenant_policy_config }} and {{ extra_policy }}.",
		"Additional says {{ tenant_policy_config }} and {{ extra_policy }}.",
		"Tenant: {{ tenant_policy_config }}\nAdditional: {{ extra_policy }}",
		"Review contract.",
	)
	want := "Tenant: Tenant says {{ tenant_policy_config }} and {{ extra_policy }}.\nAdditional: Additional says {{ tenant_policy_config }} and {{ extra_policy }}.\n\nReview contract.\n"
	if got != want {
		t.Fatalf("literal placeholder preservation = %q, want %q", got, want)
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

// TestRenderGuardianRejectionMatchesRust mirrors Rust
// prompts::guardian_instructions_tests::rejection_feedback_trims_rationale_and
// _preserves_instruction_overrides: the rationale is trimmed (an empty one
// becomes Rust's default text) and the instructions are preserved verbatim.
func TestRenderGuardianRejectionMatchesRust(t *testing.T) {
	for _, testCase := range []struct {
		rationale    string
		instructions string
		want         string
	}{
		{
			rationale:    " \nSensitive data would leave the workspace.\t ",
			instructions: " Ask for approval.\n",
			want:         "This action was rejected due to unacceptable risk.\nReason: Sensitive data would leave the workspace.\n Ask for approval.\n",
		},
		{
			rationale:    " \n\t",
			instructions: "",
			want:         "This action was rejected due to unacceptable risk.\nReason: Auto-reviewer denied the action without a specific rationale.\n",
		},
	} {
		if got := RenderGuardianRejection(testCase.rationale, testCase.instructions); got != testCase.want {
			t.Fatalf("RenderGuardianRejection(%q, %q) = %q, want %q", testCase.rationale, testCase.instructions, got, testCase.want)
		}
	}
}

// TestBundledAutoReviewInstructionsAreVendored pins the bundled fallback text
// Rust's ResolvedAutoReviewMessages supplies when the model catalog omits the
// field.
func TestBundledAutoReviewInstructionsAreVendored(t *testing.T) {
	if got := GuardianRejectionInstructions(); !strings.HasPrefix(got, "The agent must not attempt to achieve the same outcome via workaround,") ||
		!strings.HasSuffix(got, "Otherwise, stop and request user input.") {
		t.Fatalf("GuardianRejectionInstructions() = %q", got)
	}
	if got := GuardianTimeoutInstructions(); !strings.HasPrefix(got, "The automatic permission approval review did not finish before its deadline.") ||
		!strings.HasSuffix(got, "ask the user for guidance or explicit approval.") {
		t.Fatalf("GuardianTimeoutInstructions() = %q", got)
	}
	if got := GuardianTimeoutRationale(); got != "Automatic approval review timed out while evaluating the requested approval." {
		t.Fatalf("GuardianTimeoutRationale() = %q", got)
	}
}
