package sandbox

import (
	"codex_go/safety"
)

type WindowsSandboxLevel string

const (
	WindowsSandboxDisabled   WindowsSandboxLevel = "disabled"
	WindowsSandboxElevated   WindowsSandboxLevel = "elevated"
	WindowsSandboxDefault    WindowsSandboxLevel = "default"
	WindowsSandboxUnelevated WindowsSandboxLevel = "unelevated"
	// WindowsSandboxMxc mirrors Rust's WindowsSandboxLevel::Mxc. The level exists
	// for identity/tag parity; Go does not select it because the native MXC
	// launcher is not ported (Rust #45176).
	WindowsSandboxMxc WindowsSandboxLevel = "mxc"
)

func PermissionProfileSandboxTag(permissionKind safety.PermissionProfileKind, policy safety.FileSystemPolicy, networkEnabled bool, windowsLevel WindowsSandboxLevel, platformSandbox string) string {
	switch permissionKind {
	case safety.PermissionDisabled:
		return "none"
	case safety.PermissionExternal:
		return "external"
	}
	requiresPlatformSandbox := !policy.FullDiskWrite || networkEnabled
	if !requiresPlatformSandbox {
		return "none"
	}
	if windowsLevel == WindowsSandboxElevated {
		return "windows_elevated"
	}
	if windowsLevel == WindowsSandboxMxc {
		return "windows_mxc"
	}
	if platformSandbox == "" || windowsLevel == WindowsSandboxDisabled {
		return "none"
	}
	return platformSandbox
}

func PermissionProfilePolicyTag(permissionKind safety.PermissionProfileKind, policy safety.FileSystemPolicy, cwd string) string {
	return safety.PermissionProfilePolicyTag(permissionKind, policy, cwd)
}

func SandboxPolicyTag(policy *SandboxPolicy, cwd string) string {
	if policy == nil {
		return "none"
	}
	switch policy.Kind {
	case SandboxDangerFullAccess:
		return "danger-full-access"
	case SandboxReadOnly:
		return "read-only"
	case SandboxWorkspaceWrite:
		if len(policy.GetWritableRootsWithCWD(cwd)) == 0 {
			return "read-only"
		}
		return "workspace-write"
	default:
		if policy.HasFullDiskWriteAccess() {
			return "external-sandbox"
		}
		return string(policy.Kind)
	}
}
