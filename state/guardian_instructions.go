package state

import (
	_ "embed"
	"strings"
)

// Bundled Guardian reviewer text, vendored from the same Rust assets:
// codex-rs/prompts/templates/guardian/{policy,policy_template}.md
// (Rust prompts::ResolvedAutoReviewMessages falls back to them when the model
// catalog carries no auto-review messages).
//
//go:embed templates/guardian/policy.md
var bundledGuardianPolicy string

//go:embed templates/guardian/policy_template.md
var bundledGuardianPolicyTemplate string

// guardianPolicyPlaceholder is Rust's TENANT_POLICY_CONFIG_PLACEHOLDER.
const guardianPolicyPlaceholder = "{{ tenant_policy_config }}"

// GuardianPolicy returns the bundled Guardian policy document.
func GuardianPolicy() string {
	return bundledGuardianPolicy
}

// GuardianPolicyTemplate returns the bundled Guardian reviewer prompt template
// whose `{{ tenant_policy_config }}` placeholder receives the resolved policy.
func GuardianPolicyTemplate() string {
	return bundledGuardianPolicyTemplate
}

// GuardianOutputContractPrompt mirrors Rust
// guardian-reviewer::guardian_output_contract_prompt: the reviewer prompt
// fragment that describes the JSON contract paired with the assessment schema.
func GuardianOutputContractPrompt() string {
	return "You may use read-only tool checks to gather any additional context you need before deciding. When you are ready to answer, your final message must be strict JSON.\n" +
		"\n" +
		"For low-risk actions, give the final answer directly: {\"outcome\":\"allow\"}.\n" +
		"\n" +
		"For anything else, use this JSON schema:\n" +
		"{\n" +
		"  \"risk_level\": \"low\" | \"medium\" | \"high\" | \"critical\",\n" +
		"  \"user_authorization\": \"unknown\" | \"low\" | \"medium\" | \"high\",\n" +
		"  \"outcome\": \"allow\" | \"deny\",\n" +
		"  \"rationale\": string\n" +
		"}"
}

// RenderGuardianPolicyInstructions mirrors Rust
// GuardianPolicyInstructions::body: the template keeps its layout, every
// `{{ tenant_policy_config }}` occurrence receives the trimmed policy, and the
// output contract terminates the instructions.
func RenderGuardianPolicyInstructions(policy, policyTemplate, outputContract string) string {
	prompt := strings.ReplaceAll(strings.TrimRight(policyTemplate, " \t\n\r"), guardianPolicyPlaceholder, strings.TrimSpace(policy))
	return prompt + "\n\n" + outputContract + "\n"
}
