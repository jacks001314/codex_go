package app

// Rust parity: codex-rs/tui/src/app_server_session/startup_launch.rs
// (#56090/#56516). The first thread of a TUI process uses invocation launch
// choices for its approval policy and approvals reviewer, letting the app
// server resolve anything the user did not choose explicitly. Later sessions
// keep the resolved-configuration behavior.

import (
	"strings"

	"codex_go/appserver"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/sandbox"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// startupApprovalSelection mirrors Rust StartupApprovalPolicy: an explicit
// invocation value, a set of configuration assignments, or "let the server
// resolve it".
type startupApprovalSelection struct {
	// policy is the explicit approval policy to send (nil means unset).
	policy any
	// configOverrides carries granular/dotted `approval_policy` assignments that
	// must travel through the request's config layer.
	configOverrides map[string]any
	// configOverride reports that the launch supplied configuration assignments
	// rather than a single policy value.
	configOverride bool
}

// launchConfigOverrides parses the launch's generic `-c key=value` overrides in
// their original order.
func launchConfigOverrides(root *cli.RootOptions) []config.Override {
	if root == nil || len(root.ConfigOverrides) == 0 {
		return nil
	}
	overrides, err := config.ParseOverrides(root.ConfigOverrides)
	if err != nil {
		return nil
	}
	return overrides
}

// launchTypedApprovalPolicy returns the approval policy chosen with the typed
// CLI flag (Rust ConfigOverrides.approval_policy).
func launchTypedApprovalPolicy(root *cli.RootOptions) string {
	if root == nil {
		return ""
	}
	return strings.TrimSpace(root.Shared.ApprovalPolicy)
}

// resolveStartupApprovalSelection mirrors StartupApprovalChoices::from_launch:
// a typed override wins, then a parseable `-c approval_policy` string, then the
// granular/dotted assignments, and finally an unset policy that the server
// resolves.
func resolveStartupApprovalSelection(typedPolicy string, overrides []config.Override) startupApprovalSelection {
	if policy := strings.TrimSpace(typedPolicy); policy != "" {
		return startupApprovalSelection{policy: policy}
	}
	merged := map[string]any{}
	config.ApplyOverrides(merged, overrides)
	value, present := merged["approval_policy"]
	if !present {
		return startupApprovalSelection{}
	}
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if codextui.ValidApprovalPolicy(text) {
			return startupApprovalSelection{policy: text}
		}
	}
	// Preserve each original assignment boundary: parent assignments replace,
	// while dotted assignments merge with the server's CLI layer.
	return startupApprovalSelection{
		configOverrides: approvalConfigOverrideAssignments(value, overrides),
		configOverride:  true,
	}
}

// approvalConfigOverrideAssignments collects `approval_policy` assignments that
// are not shadowed by a parent assignment, resolving each against the merged
// launch layer (Rust StartupApprovalPolicy::ConfigOverride).
func approvalConfigOverrideAssignments(mergedApproval any, overrides []config.Override) map[string]any {
	out := map[string]any{}
	for _, override := range overrides {
		parts := strings.Split(override.Path, ".")
		if len(parts) == 0 || parts[0] != "approval_policy" {
			continue
		}
		if launchOverrideShadowed(override.Path, overrides) {
			continue
		}
		value, ok := lookupConfigValue(mergedApproval, parts[1:])
		if !ok {
			continue
		}
		out[override.Path] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// launchOverrideShadowed reports whether a more general assignment (a dotted
// parent) already replaced this one.
func launchOverrideShadowed(path string, overrides []config.Override) bool {
	for _, parent := range overrides {
		if parent.Path == path {
			continue
		}
		if strings.HasPrefix(path, parent.Path+".") {
			return true
		}
	}
	return false
}

// lookupConfigValue walks a merged configuration value by dotted keys.
func lookupConfigValue(value any, keys []string) (any, bool) {
	current := value
	for _, key := range keys {
		table, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := table[key]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

// applyStartupApprovalSelection stores the launch choice on a thread/start
// request, dropping the resolved config value first.
func applyStartupApprovalSelection(params *appserver.ThreadStartParams, selection startupApprovalSelection) {
	if params == nil {
		return
	}
	if params.Config != nil {
		delete(params.Config, "approval_policy")
	}
	if selection.configOverride {
		if params.Config == nil {
			params.Config = map[string]any{}
		}
		for path, value := range selection.configOverrides {
			config.ApplyOverrides(params.Config, []config.Override{{Path: path, Value: value}})
		}
	}
	params.ApprovalPolicy = selection.policy
}

// applyStartupApprovalsReviewer forwards only an explicit `-c
// approvals_reviewer` choice, leaving the server's default in place otherwise
// (Rust StartupApprovalsReviewer::Invocation).
func applyStartupApprovalsReviewer(params *appserver.ThreadStartParams, overrides []config.Override) {
	if params == nil {
		return
	}
	if params.Config != nil {
		delete(params.Config, "approvals_reviewer")
	}
	params.ApprovalsReviewer = launchApprovalsReviewer(overrides)
}

// launchApprovalsReviewer returns the last valid `-c approvals_reviewer`
// value, if any (repeated key-value overrides use the last value).
func launchApprovalsReviewer(overrides []config.Override) *string {
	for index := len(overrides) - 1; index >= 0; index-- {
		if overrides[index].Path != "approvals_reviewer" {
			continue
		}
		text, ok := overrides[index].Value.(string)
		if !ok {
			return nil
		}
		reviewer := strings.ToLower(strings.TrimSpace(text))
		switch reviewer {
		case string(chatwidget.ApprovalsReviewerUser), string(chatwidget.ApprovalsReviewerAutoReview):
			return &reviewer
		case "guardian_subagent":
			// Rust's ApprovalsReviewer accepts the legacy spelling as an alias for
			// auto_review and serializes the normalized value.
			normalized := string(chatwidget.ApprovalsReviewerAutoReview)
			return &normalized
		default:
			return nil
		}
	}
	return nil
}

// startupApprovalPolicyAllowed reports whether a projectless implicit approval
// policy may be selected under the managed requirements (Rust
// apply_projectless_approval_policy's allow-list check).
func startupApprovalPolicyAllowed(requirements *config.ConfigRequirements) bool {
	if requirements == nil || requirements.AllowedApprovalPolicies == nil {
		return true
	}
	for _, policy := range requirements.AllowedApprovalPolicies {
		if policy == sandbox.ApprovalGranular {
			return true
		}
	}
	return false
}
