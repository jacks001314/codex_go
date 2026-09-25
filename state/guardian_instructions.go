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

//go:embed templates/guardian/node_repl_policy.md
var bundledGuardianNodeReplPolicy string

// guardianPolicyPlaceholder is Rust's TENANT_POLICY_CONFIG_PLACEHOLDER.
const guardianPolicyPlaceholder = "{{ tenant_policy_config }}"

// guardianExtraPolicyPlaceholder is Rust's EXTRA_POLICY_PLACEHOLDER (#47125).
const guardianExtraPolicyPlaceholder = "{{ extra_policy }}"

// GuardianPolicy returns the bundled Guardian policy document.
func GuardianPolicy() string {
	return bundledGuardianPolicy
}

// GuardianPolicyTemplate returns the bundled Guardian reviewer prompt template
// whose `{{ tenant_policy_config }}` placeholder receives the resolved policy.
func GuardianPolicyTemplate() string {
	return bundledGuardianPolicyTemplate
}

// GuardianNodeReplPolicy returns the bundled node-REPL review rules
// (Rust prompts::ResolvedAutoReviewMessages::node_repl_policy).
func GuardianNodeReplPolicy() string {
	return bundledGuardianNodeReplPolicy
}

// Bundled auto-review instruction text, vendored from Rust
// codex-rs/prompts/src/model_messages/guardian.rs. Rust's
// ResolvedAutoReviewMessages falls back to these constants whenever the model
// catalog omits the field, so a catalog without auto-review messages still
// yields Rust's text.
const guardianRejectionInstructions = "The agent must not attempt to achieve the same outcome via workaround, " +
	"indirect execution, or policy circumvention. " +
	"Proceed only with a materially safer alternative, " +
	"or if the user explicitly approves the action after being informed of the risk. " +
	"Otherwise, stop and request user input."

const guardianTimeoutInstructions = "The automatic permission approval review did not finish before its deadline. " +
	"Do not assume the action is unsafe based on the timeout alone. " +
	"You may retry once, or ask the user for guidance or explicit approval."

// guardianTimeoutRationale is Rust's timed-out review rationale
// (guardian-reviewer::complete_review, GuardianReviewError::Timeout). It is
// recorded on the timed-out assessment event and emitted as the GuardianWarning
// text; the tool rejection instead carries the resolved timeout instructions.
const guardianTimeoutRationale = "Automatic approval review timed out while evaluating the requested approval."

// guardianRejectionDefaultRationale is Rust's replacement rationale when the
// reviewer denied an action without a specific rationale.
const guardianRejectionDefaultRationale = "Auto-reviewer denied the action without a specific rationale."

// GuardianRejectionInstructions returns the bundled rejection instructions
// (Rust prompts::ResolvedAutoReviewMessages::rejection_instructions default).
func GuardianRejectionInstructions() string {
	return guardianRejectionInstructions
}

// GuardianTimeoutInstructions returns the bundled timeout instructions
// (Rust prompts::ResolvedAutoReviewMessages::timeout_instructions default).
func GuardianTimeoutInstructions() string {
	return guardianTimeoutInstructions
}

// GuardianTimeoutRationale returns the rationale a timed-out review records on
// its assessment event and publishes as its GuardianWarning.
func GuardianTimeoutRationale() string {
	return guardianTimeoutRationale
}

// RenderGuardianRejection mirrors Rust's codex_prompts::render_guardian_rejection:
// the rationale is trimmed (an empty one becomes Rust's default text) and the
// resolved rejection instructions terminate the feedback unchanged.
func RenderGuardianRejection(rationale, rejectionInstructions string) string {
	trimmed := strings.TrimSpace(rationale)
	if trimmed == "" {
		trimmed = guardianRejectionDefaultRationale
	}
	return "This action was rejected due to unacceptable risk.\nReason: " + trimmed + "\n" + rejectionInstructions
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
// `{{ extra_policy }}` occurrence receives the trimmed extra policy, the
// `{{ tenant_policy_config }}` split points receive the trimmed tenant policy,
// and the output contract terminates the instructions.
//
// Rust #47125 substitutes in two passes so placeholder-like text inside either
// supplied policy stays literal: the template is split on the tenant
// placeholder and only the template parts have their extra placeholder
// replaced before being joined with the tenant policy.
func RenderGuardianPolicyInstructions(policy, extraPolicy, policyTemplate, outputContract string) string {
	trimmed := strings.TrimRight(policyTemplate, " \t\n\r")
	parts := strings.Split(trimmed, guardianPolicyPlaceholder)
	for i, part := range parts {
		parts[i] = strings.ReplaceAll(part, guardianExtraPolicyPlaceholder, strings.TrimSpace(extraPolicy))
	}
	prompt := strings.Join(parts, strings.TrimSpace(policy))
	return prompt + "\n\n" + outputContract + "\n"
}
