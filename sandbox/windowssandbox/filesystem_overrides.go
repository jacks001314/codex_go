package windowssandbox

import "fmt"

// WindowsSandboxFilesystemOverrides mirrors Rust
// `codex_sandboxing::WindowsSandboxFilesystemOverrides`
// (openai/codex #60042 `Centralize Windows sandbox filesystem override
// resolution`).
//
// The elevated Windows backend consumes extra deny-read paths plus explicit
// read and write roots during setup/refresh. The unelevated restricted-token
// backend only consumes extra deny-write carveouts on top of the legacy
// allow set: its WRITE_RESTRICTED token cannot make capability-SID deny-read
// ACEs participate in read checks, so read restrictions require the elevated
// backend.
type WindowsSandboxFilesystemOverrides struct {
	ReadRoots                        []string
	ReadRootsSet                     bool
	ReadRootsIncludePlatformDefaults bool
	WriteRoots                       []string
	WriteRootsSet                    bool
	AdditionalDenyReadPaths          []string
	AdditionalDenyWritePaths         []string
}

// WindowsSandboxUsesElevatedBackend mirrors Rust
// `windows_sandbox_uses_elevated_backend`: only an explicit elevated level
// selects the elevated backend, everything else keeps the restricted-token
// (legacy) backend.
func WindowsSandboxUsesElevatedBackend(level WindowsSandboxLevel) bool {
	return level == WindowsSandboxLevelElevated
}

// SetupRootOverrides projects the overrides onto the setup request shape the
// elevated backend consumes.
func (o WindowsSandboxFilesystemOverrides) SetupRootOverrides() SetupRootOverrides {
	return SetupRootOverrides{
		ReadRoots:                        cloneStrings(o.ReadRoots),
		ReadRootsSet:                     o.ReadRootsSet,
		ReadRootsIncludePlatformDefaults: o.ReadRootsIncludePlatformDefaults,
		WriteRoots:                       cloneStrings(o.WriteRoots),
		WriteRootsSet:                    o.WriteRootsSet,
		DenyReadPaths:                    cloneStrings(o.AdditionalDenyReadPaths),
		DenyWritePaths:                   cloneStrings(o.AdditionalDenyWritePaths),
	}
}

// ResolveWindowsSandboxFilesystemOverrides is the single decision point for
// "which Windows filesystem overrides does this sandbox level need?", mirroring
// Rust `WindowsSandboxFilesystemOverrides::resolve`. Rust replaced three
// hand-rolled copies of this branch (core, exec-server and direct-spawn sandbox
// preparation); Go had three copies of the restricted-token validation plus a
// per-caller level switch, which this function replaces.
//
// Resolution order is the contract callers depend on:
//
//  1. a disabled sandbox resolves no overrides,
//  2. an elevated level always selects the elevated resolver (deny-read and
//     restricted read policies are legal there),
//  3. every other level (empty, restricted-token) selects the restricted-token
//     resolver, which refuses what that backend cannot enforce.
func ResolveWindowsSandboxFilesystemOverrides(
	level WindowsSandboxLevel,
	permissions *ResolvedWindowsSandboxPermissions,
	requested WindowsSandboxFilesystemOverrides,
) (*WindowsSandboxFilesystemOverrides, error) {
	switch level {
	case WindowsSandboxLevelDisabled:
		return nil, nil
	case "", WindowsSandboxLevelRestrictedToken, WindowsSandboxLevelElevated:
	default:
		return nil, fmt.Errorf("invalid windows sandbox level: %s", level)
	}
	if WindowsSandboxUsesElevatedBackend(level) {
		return resolveElevatedFilesystemOverrides(permissions, requested)
	}
	return resolveRestrictedTokenFilesystemOverrides(permissions, requested)
}

// resolveElevatedFilesystemOverrides keeps the caller-requested roots and
// deny paths for the elevated setup/refresh path after validating that the
// policy is enforceable there.
func resolveElevatedFilesystemOverrides(
	permissions *ResolvedWindowsSandboxPermissions,
	requested WindowsSandboxFilesystemOverrides,
) (*WindowsSandboxFilesystemOverrides, error) {
	if permissions == nil {
		return nil, fmt.Errorf("elevated Windows sandbox requires an enforceable filesystem policy")
	}
	if err := permissions.ValidateElevatedFilesystemPolicy(); err != nil {
		return nil, err
	}
	return &WindowsSandboxFilesystemOverrides{
		ReadRoots:                        cloneStrings(requested.ReadRoots),
		ReadRootsSet:                     requested.ReadRootsSet,
		ReadRootsIncludePlatformDefaults: requested.ReadRootsIncludePlatformDefaults,
		WriteRoots:                       cloneStrings(requested.WriteRoots),
		WriteRootsSet:                    requested.WriteRootsSet,
		AdditionalDenyReadPaths:          cloneStrings(requested.AdditionalDenyReadPaths),
		AdditionalDenyWritePaths:         cloneStrings(requested.AdditionalDenyWritePaths),
	}, nil
}

// resolveRestrictedTokenFilesystemOverrides mirrors Rust
// `resolve_windows_restricted_token_filesystem_overrides`: the restricted-token
// backend can only layer deny-write carveouts on the legacy allow set, so a
// policy that restricts reads or denies paths is refused instead of silently
// running unsandboxed.
func resolveRestrictedTokenFilesystemOverrides(
	permissions *ResolvedWindowsSandboxPermissions,
	requested WindowsSandboxFilesystemOverrides,
) (*WindowsSandboxFilesystemOverrides, error) {
	if permissions == nil {
		return nil, fmt.Errorf("restricted-token Windows sandbox requires an enforceable filesystem policy")
	}
	// Order matters: the split-read check runs before the deny-read check so a
	// policy that restricts reads reports the restricted-read reason even when
	// it also carries deny entries.
	if !permissions.HasFullDiskReadAccess() {
		return nil, fmt.Errorf("restricted read-only access requires the elevated Windows sandbox backend")
	}
	if len(requested.AdditionalDenyReadPaths) > 0 {
		return nil, fmt.Errorf("deny-read overrides require the elevated Windows sandbox backend")
	}
	return &WindowsSandboxFilesystemOverrides{
		AdditionalDenyWritePaths: cloneStrings(requested.AdditionalDenyWritePaths),
	}, nil
}
