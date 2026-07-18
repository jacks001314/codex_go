package sandbox

import (
	"fmt"
	"sort"
	"strings"
)

type PermissionPromptSandboxMode string

const (
	PermissionPromptDangerFullAccess PermissionPromptSandboxMode = "danger-full-access"
	PermissionPromptWorkspaceWrite   PermissionPromptSandboxMode = "workspace-write"
	PermissionPromptReadOnly         PermissionPromptSandboxMode = "read-only"
)

type PermissionPromptNetworkAccess string

const (
	PermissionPromptNetworkEnabled    PermissionPromptNetworkAccess = "enabled"
	PermissionPromptNetworkRestricted PermissionPromptNetworkAccess = "restricted"
)

type PermissionPromptApprovalPolicy string

const (
	PermissionPromptApprovalNever         PermissionPromptApprovalPolicy = "never"
	PermissionPromptApprovalUnlessTrusted PermissionPromptApprovalPolicy = "unless_trusted"
	PermissionPromptApprovalOnRequest     PermissionPromptApprovalPolicy = "on_request"
	PermissionPromptApprovalGranular      PermissionPromptApprovalPolicy = "granular"
)

type PermissionPromptReviewer string

const PermissionPromptAutoReview PermissionPromptReviewer = "auto_review"

type PermissionPromptConfig struct {
	SandboxMode                    PermissionPromptSandboxMode
	NetworkAccess                  PermissionPromptNetworkAccess
	ApprovalPolicy                 PermissionPromptApprovalPolicy
	ApprovalsReviewer              PermissionPromptReviewer
	WritableRoots                  []string
	DeniedReadPaths                []string
	DeniedReadGlobs                []string
	ApprovedCommandPrefixes        [][]string
	ExecPermissionApprovalsEnabled bool
	RequestPermissionsToolEnabled  bool
	GranularAllowedCategories      []string
	GranularAutomaticallyRejected  []string
}

func BuildPermissionPrompt(config *PermissionPromptConfig) string {
	if config == nil {
		config = &PermissionPromptConfig{}
	}
	sections := []string{
		sandboxPromptText(config.SandboxMode, config.NetworkAccess),
		approvalPromptText(config),
	}
	if roots := writableRootsText(config.WritableRoots); roots != "" {
		sections = append(sections, roots)
	}
	if denied := deniedReadsText(config.DeniedReadPaths, config.DeniedReadGlobs); denied != "" {
		sections = append(sections, denied)
	}
	text := strings.Join(nonEmpty(sections), "\n\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}

func sandboxPromptText(mode PermissionPromptSandboxMode, network PermissionPromptNetworkAccess) string {
	if mode == "" {
		mode = PermissionPromptWorkspaceWrite
	}
	if network == "" {
		network = PermissionPromptNetworkRestricted
	}
	switch mode {
	case PermissionPromptDangerFullAccess:
		return "Filesystem sandboxing is disabled. Network access is " + string(network) + "."
	case PermissionPromptReadOnly:
		return "Filesystem sandboxing is read-only. Network access is " + string(network) + "."
	default:
		return "Filesystem sandboxing is workspace-write. Network access is " + string(network) + "."
	}
}

func approvalPromptText(config *PermissionPromptConfig) string {
	switch config.ApprovalPolicy {
	case PermissionPromptApprovalNever:
		return "Approval policy is `never`: do not request escalated permissions."
	case PermissionPromptApprovalUnlessTrusted:
		return withRequestPermissions("Approval policy is `unless_trusted`: request approval when sandboxed work needs elevated access.", config.RequestPermissionsToolEnabled)
	case PermissionPromptApprovalGranular:
		return granularText(config)
	default:
		sections := []string{"Approval policy is `on_request`: request approval when a command or filesystem action needs elevated permissions."}
		if config.ExecPermissionApprovalsEnabled {
			sections = append(sections, "Use shell permission approval for later shell-like commands that need it.")
		}
		if config.RequestPermissionsToolEnabled {
			sections = append(sections, requestPermissionsToolText())
		}
		if prefixes := approvedPrefixesText(config.ApprovedCommandPrefixes); prefixes != "" {
			sections = append(sections, prefixes)
		}
		if config.ApprovalsReviewer == PermissionPromptAutoReview {
			sections = append(sections, autoReviewText())
		}
		return strings.Join(sections, "\n\n")
	}
}

func granularText(config *PermissionPromptConfig) string {
	sections := []string{"# Approval Requests\n\nApproval policy is `granular`. Categories set to false are automatically rejected instead of prompting the user."}
	if len(config.GranularAllowedCategories) > 0 {
		sections = append(sections, "These approval categories may still prompt the user when needed:\n"+bulletList(config.GranularAllowedCategories))
	}
	if len(config.GranularAutomaticallyRejected) > 0 {
		sections = append(sections, "These approval categories are automatically rejected instead of prompting the user:\n"+bulletList(config.GranularAutomaticallyRejected))
	}
	if config.RequestPermissionsToolEnabled {
		sections = append(sections, requestPermissionsToolText())
	}
	if prefixes := approvedPrefixesText(config.ApprovedCommandPrefixes); prefixes != "" {
		sections = append(sections, prefixes)
	}
	if config.ApprovalsReviewer == PermissionPromptAutoReview {
		sections = append(sections, autoReviewText())
	}
	return strings.Join(sections, "\n\n")
}

func withRequestPermissions(text string, enabled bool) string {
	if !enabled {
		return text
	}
	return text + "\n\n" + requestPermissionsToolText()
}

func requestPermissionsToolText() string {
	return "# request_permissions Tool\n\nThe built-in `request_permissions` tool is available in this session. Request only the permissions required for the task."
}

func autoReviewText() string {
	return "`approvals_reviewer` is `auto_review`: sandbox escalations with require_escalated will be reviewed for policy compliance."
}

func writableRootsText(roots []string) string {
	if len(roots) == 0 {
		return ""
	}
	roots = append([]string(nil), roots...)
	sort.Strings(roots)
	wrapped := make([]string, len(roots))
	for i, root := range roots {
		wrapped[i] = "`" + root + "`"
	}
	if len(wrapped) == 1 {
		return "The writable root is " + wrapped[0] + "."
	}
	return "The writable roots are " + strings.Join(wrapped, ", ") + "."
}

func deniedReadsText(paths []string, globs []string) string {
	var entries []string
	for _, path := range paths {
		entries = append(entries, "- path `"+path+"`")
	}
	for _, glob := range globs {
		entries = append(entries, "- glob `"+glob+"`")
	}
	if len(entries) == 0 {
		return ""
	}
	sort.Strings(entries)
	return "## Denied filesystem reads\nThe active permission profile denies reading these paths/globs. Do not request escalation or additional permissions to read them; these denials are policy restrictions.\n" + strings.Join(entries, "\n")
}

func approvedPrefixesText(prefixes [][]string) string {
	if len(prefixes) == 0 {
		return ""
	}
	formatted := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if len(prefix) == 0 {
			continue
		}
		quoted := make([]string, len(prefix))
		for i, part := range prefix {
			quoted[i] = fmt.Sprintf("%q", part)
		}
		formatted = append(formatted, "["+strings.Join(quoted, ", ")+"]")
	}
	if len(formatted) == 0 {
		return ""
	}
	sort.Strings(formatted)
	return "## Approved command prefixes\nThe following prefix rules have already been approved: " + strings.Join(formatted, ", ")
}

func bulletList(values []string) string {
	values = append([]string(nil), values...)
	sort.Strings(values)
	var lines []string
	for _, value := range values {
		lines = append(lines, "- `"+value+"`")
	}
	return strings.Join(lines, "\n")
}

func nonEmpty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}
