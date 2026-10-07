package sandbox

import (
	"os"
	"runtime"

	"codex_go/sandbox/sandboxpath"
	"codex_go/utils"
)

// SystemBwrapWarning reports why the profile's sandboxed commands would fall
// back to a limited (or unavailable) sandbox, or "" when the profile does not
// need bubblewrap or the system launcher is usable (Rust
// sandboxing::bwrap::system_bwrap_warning). Only the *system* launcher is
// inspected: the message itself points at the bundled bubblewrap the sandbox
// falls back to.
func SystemBwrapWarning(profile *PermissionProfile) string {
	return SystemBwrapWarningForCWD(profile, currentWorkingDirectory())
}

// SystemBwrapWarningForCWD is SystemBwrapWarning with the policy working
// directory the sandbox resolves writable roots against (Rust #51211 passes
// `config.cwd`). The launcher probe uses the command's effective profile, so a
// bwrap inside a writable root no longer counts as the system launcher.
func SystemBwrapWarningForCWD(profile *PermissionProfile, sandboxPolicyCWD string) string {
	if runtime.GOOS != "linux" {
		// The non-Linux Rust build has no bubblewrap warning at all.
		return ""
	}
	if !ShouldRequirePlatformSandbox(profile) {
		return ""
	}
	return systemBwrapWarning(
		findSystemBwrapInPath(os.Getenv("PATH"), currentWorkingDirectory(), preSandboxFilesystemPolicy(profile, sandboxPolicyCWD)),
		utils.IsWSL1(),
		func(path string) bool { return probeSystemBwrapUserNamespaces(path, systemBwrapProbeTimeout) },
	)
}

// preSandboxFilesystemPolicy mirrors the filesystem-policy view Rust
// `PermissionProfile::file_system_sandbox_policy` hands the pre-sandbox PATH
// filter: a full-disk-write policy enumerates no writable roots.
func preSandboxFilesystemPolicy(profile *PermissionProfile, sandboxPolicyCWD string) sandboxpath.FilesystemPolicy {
	if profile == nil || profile.SandboxPolicy == nil {
		return sandboxpath.FilesystemPolicy{}
	}
	if profile.SandboxPolicy.HasFullDiskWriteAccess() {
		return sandboxpath.FilesystemPolicy{FullDiskWriteAccess: true}
	}
	return sandboxpath.FilesystemPolicy{WritableRoots: profile.SandboxPolicy.GetWritableRootsWithCWD(sandboxPolicyCWD)}
}

func currentWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

// ShouldRequirePlatformSandbox mirrors Rust
// sandboxing::policy_transforms::should_require_platform_sandbox: the platform
// sandbox is required when the profile restricts the filesystem or the network,
// and an unrestricted or executor-managed profile never needs one.
func ShouldRequirePlatformSandbox(profile *PermissionProfile) bool {
	// The startup warning has no managed network requirements (Rust passes
	// false at that call site).
	policy := profile.LegacySandboxPolicy()
	externalSandbox := policy != nil && policy.Kind == SandboxModeExternalSandbox
	if !profile.AllowsNetwork() {
		// Restricted filesystem without network: every kind except an external
		// sandbox has to be enforced by the platform.
		return !externalSandbox
	}
	if policy == nil || externalSandbox || policy.Kind == SandboxDangerFullAccess {
		// Unrestricted and external sandboxes need no platform sandbox.
		return false
	}
	// Restricted: a profile that already grants full disk write access does not
	// need bubblewrap either.
	return !policy.HasFullDiskWriteAccess()
}
