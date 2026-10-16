// Rust #54383 (upstream b095b2ef23) and #54385 (upstream 36803b180a):
// `/permissions <profile-id> [--approval-policy=<policy>] [--reviewer=user|auto_review]`
// selects a permission profile inline instead of only opening the picker. The
// argument grammar is codex-rs/tui/src/chatwidget/permission_discovery.rs:20-60,
// which tokenizes the request with the shared parser of
// chatwidget/slash_command_args.rs (see slash_command_args.go).
package tea

import (
	"encoding/json"

	"codex_go/sandbox"
	chatwidget "codex_go/tui/chatwidget"
)

const permissionProfileUsageMessage = "Usage: /permissions <profile-id> [--approval-policy=<policy>] [--reviewer=user|auto_review]"

// permissionProfileArgs is the parsed `/permissions` request (Rust
// PermissionProfileArgs, permission_discovery.rs:14-18).
type permissionProfileArgs struct {
	profileID         string
	approvalPolicy    string
	hasApprovalPolicy bool
	reviewer          chatwidget.ApprovalsReviewer
	hasReviewer       bool
}

// parsePermissionProfileArgs mirrors parse_permission_profile_args
// (permission_discovery.rs:43-60): `--approval-policy` and `--reviewer` are value
// settings, there are no switches or boolean settings, and only the first
// positional is the profile. The returned string is the Rust error message, empty
// on success.
func parsePermissionProfileArgs(args string) (permissionProfileArgs, string) {
	parsed, failure := parseSlashCommandArgs(args, []string{"--approval-policy", "--reviewer"}, nil, nil)
	if failure != "" {
		return permissionProfileArgs{}, failure
	}
	if len(parsed.positionals) == 0 {
		return permissionProfileArgs{}, permissionProfileUsageMessage
	}
	if len(parsed.positionals) > 1 {
		return permissionProfileArgs{}, "Unexpected permission argument `" + parsed.positionals[1] + "`."
	}
	request := permissionProfileArgs{profileID: parsed.positionals[0]}
	if value, ok := parsed.value("--approval-policy"); ok {
		policy, failure := parsePermissionApprovalPolicy(value)
		if failure != "" {
			return permissionProfileArgs{}, failure
		}
		request.approvalPolicy, request.hasApprovalPolicy = policy, true
	}
	if value, ok := parsed.value("--reviewer"); ok {
		reviewer, failure := parsePermissionReviewer(value)
		if failure != "" {
			return permissionProfileArgs{}, failure
		}
		request.reviewer, request.hasReviewer = reviewer, true
	}
	return request, ""
}

// parsePermissionApprovalPolicy mirrors parse_approval_policy
// (permission_discovery.rs:20-31): the three named policies, otherwise a granular
// policy in JSON. A Go approval policy travels as the scalar string the app
// server already uses for granular profiles (appserver/runtime_router.go:15544-15591
// resolves `{"granular": …}` to "granular"), so a granular request resolves to
// that scalar; the granular flags themselves stay with the config layer.
func parsePermissionApprovalPolicy(value string) (string, string) {
	switch value {
	case string(sandbox.ApprovalUnlessTrusted), string(sandbox.ApprovalOnRequest), string(sandbox.ApprovalNever):
		return value, ""
	}
	if isGranularPermissionApprovalPolicy(value) {
		return string(sandbox.ApprovalGranular), ""
	}
	return "", "Unknown approval policy `" + value + "`. Expected `untrusted`, `on-request`, `never`, or a granular policy."
}

// isGranularPermissionApprovalPolicy reports whether value is the externally
// tagged granular policy Rust deserializes into AskForApproval
// (app-server-protocol/src/protocol/v2/shared.rs:206-221): exactly one key with
// an object payload carrying the three required boolean flags.
func isGranularPermissionApprovalPolicy(value string) bool {
	var policy map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &policy); err != nil || len(policy) != 1 {
		return false
	}
	raw, ok := policy[string(sandbox.ApprovalGranular)]
	if !ok {
		return false
	}
	var flags map[string]json.RawMessage
	if err := json.Unmarshal(raw, &flags); err != nil {
		return false
	}
	for _, required := range []string{"sandbox_approval", "rules", "mcp_elicitations"} {
		field, ok := flags[required]
		if !ok {
			return false
		}
		var flag bool
		if err := json.Unmarshal(field, &flag); err != nil {
			return false
		}
	}
	return true
}

// parsePermissionReviewer mirrors parse_reviewer (permission_discovery.rs:33-41).
func parsePermissionReviewer(value string) (chatwidget.ApprovalsReviewer, string) {
	switch chatwidget.ApprovalsReviewer(value) {
	case chatwidget.ApprovalsReviewerUser, chatwidget.ApprovalsReviewerAutoReview:
		return chatwidget.ApprovalsReviewer(value), ""
	default:
		return "", "Unknown permission reviewer `" + value + "`. Expected `user` or `auto_review`."
	}
}
