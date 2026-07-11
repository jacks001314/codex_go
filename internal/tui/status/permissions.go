package status

import (
	"strings"

	"codex_go/internal/config"
	"codex_go/internal/sandbox"
)

func StatusApprovalLabel(approvalPolicy sandbox.AskForApproval, approvalsReviewer config.ApprovalsReviewer, approval string) string {
	if approvalPolicy == sandbox.ApprovalOnRequest {
		if approvalsReviewer == config.ApprovalsReviewerAutoReview {
			return "Approve for me"
		}
		return "Ask for approval"
	}
	return approval
}

func StatusPermissionsLabel(activePermissionProfile *sandbox.ActivePermissionProfile, permissionProfile *sandbox.PermissionProfile, approvalPolicy sandbox.AskForApproval, sandboxLabel string, approval string, workspaceRootSuffix string) string {
	activeID := ""
	if activePermissionProfile != nil {
		activeID = activePermissionProfile.ID
	}
	switch normalizeStatusPermissionProfileID(activeID) {
	case sandbox.BuiltInPermissionProfileReadOnly:
		label := "Read Only"
		if sandboxLabel == "read-only with network access" {
			label = "Read Only with network access"
		}
		return label + " (" + approval + ")"
	case sandbox.BuiltInPermissionProfileWorkspace:
		switch sandboxLabel {
		case "workspace":
			return "Workspace" + workspaceRootSuffix + " (" + approval + ")"
		case "workspace with network access":
			return "Workspace with network access" + workspaceRootSuffix + " (" + approval + ")"
		}
	case sandbox.BuiltInPermissionProfileDangerFullAccess:
		if permissionProfile != nil && permissionProfile.Disabled {
			if approvalPolicy == sandbox.ApprovalNever {
				return "Full Access"
			}
			return "No Sandbox (" + approval + ")"
		}
	case "":
	default:
		return "Profile " + activeID + " (" + decorateWorkspaceSandboxLabel(sandboxLabel, workspaceRootSuffix) + ", " + approval + ")"
	}

	if sandboxLabel == "read-only" {
		return "Read Only (" + approval + ")"
	}
	if approvalPolicy == sandbox.ApprovalOnRequest && sandboxLabel == "workspace" {
		return "Workspace" + workspaceRootSuffix + " (" + approval + ")"
	}
	if approvalPolicy == sandbox.ApprovalNever && permissionProfile != nil && permissionProfile.Disabled {
		return "Full Access"
	}
	return "Custom (" + decorateWorkspaceSandboxLabel(sandboxLabel, workspaceRootSuffix) + ", " + approval + ")"
}

func decorateWorkspaceSandboxLabel(sandboxLabel string, workspaceRootSuffix string) string {
	if workspaceRootSuffix != "" && strings.HasPrefix(sandboxLabel, "workspace") {
		return sandboxLabel + workspaceRootSuffix
	}
	return sandboxLabel
}

func normalizeStatusPermissionProfileID(id string) string {
	switch strings.TrimSpace(id) {
	case sandbox.BuiltInPermissionProfileReadOnly, ":" + sandbox.BuiltInPermissionProfileReadOnly:
		return sandbox.BuiltInPermissionProfileReadOnly
	case sandbox.BuiltInPermissionProfileWorkspace, ":" + sandbox.BuiltInPermissionProfileWorkspace, "workspace-write", "auto":
		return sandbox.BuiltInPermissionProfileWorkspace
	case sandbox.BuiltInPermissionProfileDangerFullAccess, ":" + sandbox.BuiltInPermissionProfileDangerFullAccess, "full-access":
		return sandbox.BuiltInPermissionProfileDangerFullAccess
	default:
		return strings.TrimSpace(id)
	}
}
