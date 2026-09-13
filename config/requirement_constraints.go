package config

import (
	"fmt"
	"strings"

	"codex_go/codexapi"
	featureflags "codex_go/features"
	"codex_go/sandbox"
)

// Rust parity: codex-rs/core/src/config/mod.rs calls
// apply_requirement_constrained_value for approval_policy, approvals_reviewer,
// permission_profile, windows.sandbox, and web_search_mode. The managed
// allow-list constrains the configured value; a value it disallows falls back
// to the requirement's own default (the first allowed entry for the approval
// policy and approvals reviewer, the preferred allowed mode for web search) and
// produces a source-aware startup warning carrying the constraint error.
//
// The permission-profile and windows.sandbox fallbacks live next to their
// resolvers (config/permissions.go, config/windows_sandbox_mode.go); this file
// covers the three plain enum-shaped values.
//
// Go's requirements model does not carry Rust's typed RequirementSource, so the
// warnings name "managed requirements" as the origin.

// applyManagedConstrainedOverrides applies the requirement-constrained values to
// the effective configuration, mirroring Rust
// apply_requirement_constrained_value for the enum-shaped fields.
func applyManagedConstrainedOverrides(values map[string]any, requirements *ConfigRequirements) error {
	if values == nil || requirements == nil {
		return nil
	}
	if requirements.AllowedApprovalPolicies != nil {
		effective, _, err := resolveRequirementConstrainedApprovalPolicy(
			configuredApprovalPolicyValue(values["approval_policy"]),
			requirements.AllowedApprovalPolicies,
		)
		if err != nil {
			return err
		}
		if effective != "" {
			values["approval_policy"] = effective
		}
	}
	if requirements.AllowedApprovalsReviewers != nil {
		effective, _, err := resolveRequirementConstrainedApprovalsReviewer(
			configuredApprovalsReviewerValue(values["approvals_reviewer"]),
			requirements.AllowedApprovalsReviewers,
		)
		if err != nil {
			return err
		}
		if effective != "" {
			values["approvals_reviewer"] = effective
		}
	}
	if requirements.AllowedWebSearchModes != nil {
		effective, _, err := resolveRequirementConstrainedWebSearchMode(
			webSearchModeForConfigValues(values),
			requirements.AllowedWebSearchModes,
		)
		if err != nil {
			return err
		}
		values["web_search"] = string(effective)
	}
	return nil
}

// configuredApprovalPolicyValue reads the configured approval policy, including
// the granular table form.
func configuredApprovalPolicyValue(raw any) string {
	switch value := raw.(type) {
	case string:
		return value
	case map[string]any:
		if _, ok := value["granular"]; ok {
			return string(sandbox.ApprovalGranular)
		}
		if text, ok := value["type"].(string); ok {
			return text
		}
	}
	return ""
}

func configuredApprovalsReviewerValue(raw any) string {
	if value, ok := raw.(string); ok {
		return value
	}
	return ""
}

// webSearchModeForConfigValues mirrors Rust resolve_web_search_mode: the
// configured `web_search` value wins, then the web-search feature flags, then
// the cached default.
func webSearchModeForConfigValues(values map[string]any) codexapi.WebSearchMode {
	if values != nil {
		if raw, ok := values["web_search"]; ok && raw != nil {
			return codexapi.WebSearchModeFromValue(raw)
		}
	}
	settings := (&Config{Values: values}).FeatureSettings()
	if featureflags.Enabled(settings, "web_search_cached") {
		return codexapi.WebSearchModeCached
	}
	if featureflags.Enabled(settings, "web_search_request") {
		return codexapi.WebSearchModeLive
	}
	return codexapi.WebSearchModeCached
}

// resolveRequirementConstrainedApprovalPolicy mirrors Rust's
// Constrained<AskForApproval> built from allowed_approval_policies: the first
// listed policy is the requirement default, and an explicit disallowed value
// falls back to it. An unset configured value resolves to the fallback without
// a warning, matching Rust's implicit-default path.
func resolveRequirementConstrainedApprovalPolicy(configured string, allowed []sandbox.AskForApproval) (string, string, error) {
	if allowed == nil {
		return strings.TrimSpace(configured), "", nil
	}
	if len(allowed) == 0 {
		return "", "", fmt.Errorf("requirements.toml allowed_approval_policies cannot be empty")
	}
	normalized := make([]string, 0, len(allowed))
	for _, policy := range allowed {
		normalized = append(normalized, normalizeRequirementApprovalPolicy(string(policy)))
	}
	fallback := normalized[0]
	selected := normalizeRequirementApprovalPolicy(configured)
	if selected == "" {
		return fallback, "", nil
	}
	if stringInRequirementList(normalized, selected) {
		return selected, "", nil
	}
	return fallback, requirementConstrainedWarning(
		"approval_policy",
		rustApprovalPolicyDebug(selected),
		joinRequirementDebug(normalized, rustApprovalPolicyDebug),
		rustApprovalPolicyDebug(fallback),
	), nil
}

// resolveRequirementConstrainedApprovalsReviewer mirrors Rust's
// Constrained<ApprovalsReviewer> built from allowed_approvals_reviewers.
func resolveRequirementConstrainedApprovalsReviewer(configured string, allowed []ApprovalsReviewer) (string, string, error) {
	if allowed == nil {
		return strings.TrimSpace(configured), "", nil
	}
	if len(allowed) == 0 {
		return "", "", fmt.Errorf("requirements.toml allowed_approvals_reviewers cannot be empty")
	}
	normalized := make([]string, 0, len(allowed))
	for _, reviewer := range allowed {
		normalized = append(normalized, normalizeRequirementApprovalsReviewer(string(reviewer)))
	}
	fallback := normalized[0]
	selected := normalizeRequirementApprovalsReviewer(configured)
	if selected == "" {
		return fallback, "", nil
	}
	if stringInRequirementList(normalized, selected) {
		return selected, "", nil
	}
	return fallback, requirementConstrainedWarning(
		"approvals_reviewer",
		rustApprovalsReviewerDebug(selected),
		joinRequirementDebug(normalized, rustApprovalsReviewerDebug),
		rustApprovalsReviewerDebug(fallback),
	), nil
}

// resolveRequirementConstrainedWebSearchMode mirrors Rust's
// Constrained<WebSearchMode> built from allowed_web_search_modes: disabled is
// always accepted, and the fallback prefers cached, then indexed, then live.
func resolveRequirementConstrainedWebSearchMode(configured codexapi.WebSearchMode, allowed []WebSearchMode) (codexapi.WebSearchMode, string, error) {
	if allowed == nil {
		return configured, "", nil
	}
	accepted := map[codexapi.WebSearchMode]bool{codexapi.WebSearchModeDisabled: true}
	for _, mode := range allowed {
		accepted[codexapi.WebSearchModeFromValue(string(mode))] = true
	}
	fallback := codexapi.WebSearchModeDisabled
	for _, candidate := range []codexapi.WebSearchMode{
		codexapi.WebSearchModeCached,
		codexapi.WebSearchModeIndexed,
		codexapi.WebSearchModeLive,
	} {
		if accepted[candidate] {
			fallback = candidate
			break
		}
	}
	selected := codexapi.WebSearchModeFromValue(string(configured))
	if accepted[selected] {
		return selected, "", nil
	}
	return fallback, requirementConstrainedWarning(
		"web_search_mode",
		rustWebSearchModeDebug(string(selected)),
		joinRequirementDebug(acceptedWebSearchModes(accepted), rustWebSearchModeDebug),
		rustWebSearchModeDebug(string(fallback)),
	), nil
}

// requirementConstrainedWarning builds Rust's
// apply_requirement_constrained_value message (including the ConstraintError
// detail text).
func requirementConstrainedWarning(field, candidateDebug, allowedDebug, fallbackDebug string) string {
	return fmt.Sprintf(
		"Configured value for `%s` is disallowed by requirements; falling back to required value %s. Details: invalid value for `%s`: `%s` is not in the allowed set [%s] (set by %s)",
		field, fallbackDebug, field, candidateDebug, allowedDebug, managedRequirementSource)
}

func stringInRequirementList(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func joinRequirementDebug(values []string, render func(string) string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, render(value))
	}
	return strings.Join(parts, ", ")
}

// acceptedWebSearchModes orders the accepted modes the way Rust's BTreeSet
// over WebSearchModeRequirement does.
func acceptedWebSearchModes(accepted map[codexapi.WebSearchMode]bool) []string {
	var out []string
	for _, mode := range []codexapi.WebSearchMode{
		codexapi.WebSearchModeDisabled,
		codexapi.WebSearchModeCached,
		codexapi.WebSearchModeIndexed,
		codexapi.WebSearchModeLive,
	} {
		if accepted[mode] {
			out = append(out, string(mode))
		}
	}
	return out
}

// normalizeRequirementApprovalPolicy mirrors Rust's AskForApproval
// deserialization aliases plus Go's legacy spellings.
func normalizeRequirementApprovalPolicy(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch normalized {
	case string(sandbox.ApprovalNever):
		return string(sandbox.ApprovalNever)
	case string(sandbox.ApprovalOnRequest), "on-failure", "onrequest":
		return string(sandbox.ApprovalOnRequest)
	case string(sandbox.ApprovalUnlessTrusted), "unless-trusted", "unlesstrusted":
		return string(sandbox.ApprovalUnlessTrusted)
	case string(sandbox.ApprovalGranular):
		return string(sandbox.ApprovalGranular)
	default:
		return normalized
	}
}

// normalizeRequirementApprovalsReviewer mirrors Rust's ApprovalsReviewer
// aliases (guardian_subagent is the legacy auto_review spelling).
func normalizeRequirementApprovalsReviewer(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case string(ApprovalsReviewerGuardianSubagent), string(ApprovalsReviewerAutoReview), "autoreview":
		return string(ApprovalsReviewerAutoReview)
	case string(ApprovalsReviewerUser), "":
		return string(ApprovalsReviewerUser)
	default:
		return normalized
	}
}

// rustApprovalPolicyDebug renders AskForApproval the way Rust's derived Debug
// does (the warning embeds the Rust Debug form).
func rustApprovalPolicyDebug(value string) string {
	switch value {
	case string(sandbox.ApprovalOnRequest):
		return "OnRequest"
	case string(sandbox.ApprovalNever):
		return "Never"
	case string(sandbox.ApprovalUnlessTrusted):
		return "UnlessTrusted"
	case string(sandbox.ApprovalGranular):
		return "Granular"
	default:
		return value
	}
}

func rustApprovalsReviewerDebug(value string) string {
	switch value {
	case string(ApprovalsReviewerAutoReview):
		return "AutoReview"
	case string(ApprovalsReviewerUser):
		return "User"
	default:
		return value
	}
}

func rustWebSearchModeDebug(value string) string {
	switch codexapi.WebSearchMode(value) {
	case codexapi.WebSearchModeDisabled:
		return "Disabled"
	case codexapi.WebSearchModeCached:
		return "Cached"
	case codexapi.WebSearchModeIndexed:
		return "Indexed"
	case codexapi.WebSearchModeLive:
		return "Live"
	default:
		return value
	}
}
