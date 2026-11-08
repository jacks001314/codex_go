package sandbox

import (
	"fmt"
	"strings"

	"codex_go/utils"
)

// CanWrite reports whether the entry grants write access. Rust
// `FileSystemAccessMode::can_write`.
func (a FileSystemAccessMode) CanWrite() bool {
	return a == FileSystemAccessWrite
}

// LegacyWritablePath projects an explicit writable entry for the legacy
// native-path sandbox adapter (Rust #59933
// `FileSystemSandboxEntry::legacy_writable_path`). Special paths and glob
// patterns stay with the adapter's own handling, so they report ok=false
// instead of a value; a path that is not a local absolute path is an error
// because the adapter must not silently drop it.
func LegacyWritablePath(entry FileSystemSandboxEntry) (path string, ok bool, err error) {
	if !entry.Access.CanWrite() {
		return "", false, nil
	}
	switch entry.Path.Type {
	case "path", "":
		projected, err := legacyWritablePath(entry.Path.Path)
		if err != nil {
			return "", false, err
		}
		return projected, true, nil
	default:
		return "", false, nil
	}
}

// PermissionProfileHasFullDiskWriteAccess reports whether the profile itself
// grants full disk write access. A nil profile declares no capability, so it is
// not full access; the settings and turn paths share this predicate so a
// protected model cannot be judged differently by the two entry points
// (Rust #59933 keeps the effective permission profile authoritative).
func PermissionProfileHasFullDiskWriteAccess(profile *PermissionProfile) bool {
	if profile == nil {
		return false
	}
	policy := profile.LegacySandboxPolicy()
	return policy != nil && policy.HasFullDiskWriteAccess()
}

// RequiresProtectedModelWorkspaceWriteDowngrade reports whether a model that
// requires automatic review forces this profile down to `workspace-write`
// (Rust #59933, core/src/session/mod.rs). The profile keeps its permissions
// when external enforcement owns the boundary, when network access is
// restricted, or when a writable path cannot be projected to a local path; the
// legacy adapter would otherwise widen or misplace the downgraded roots.
func (p *PermissionProfile) RequiresProtectedModelWorkspaceWriteDowngrade() bool {
	if p == nil {
		return false
	}
	policy := p.LegacySandboxPolicy()
	if policy == nil || policy.Kind == SandboxModeExternalSandbox {
		return false
	}
	if !profileGrantsNetwork(p) {
		return false
	}
	if !PermissionProfileHasFullDiskWriteAccess(p) {
		return false
	}
	for _, root := range policy.WritableRoots {
		if _, err := legacyWritablePath(root); err != nil {
			return false
		}
	}
	return true
}

// legacyWritablePath rejects the writable roots the legacy native-path adapter
// cannot express. `:slash_tmp`, `:tmpdir` and friends are symbolic, and glob
// patterns are matched by the profile-aware adapters instead.
func legacyWritablePath(root string) (string, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return "", fmt.Errorf("writable path is empty")
	}
	if strings.HasPrefix(trimmed, ":") {
		return "", fmt.Errorf("special writable path `%s` cannot be projected locally", trimmed)
	}
	if strings.ContainsAny(trimmed, "*?[") {
		return "", fmt.Errorf("writable glob `%s` cannot be projected locally", trimmed)
	}
	uri, ok := utils.InferredPathURI(trimmed)
	if !ok {
		return "", fmt.Errorf("writable path `%s` is not an absolute local path", trimmed)
	}
	return uri.NativePathString(), nil
}

// profileGrantsNetwork reads network access from the profile instead of the
// legacy policy. A managed profile states it in NetworkEnabled; the legacy
// danger-full-access profile sets NetworkEnabled too, so an unrestricted
// file system alone never implies network access (Rust #59933).
func profileGrantsNetwork(p *PermissionProfile) bool {
	if p == nil {
		return false
	}
	if p.NetworkEnabled {
		return true
	}
	if p.SandboxPolicy == nil {
		return false
	}
	switch p.SandboxPolicy.Kind {
	case SandboxReadOnly, SandboxWorkspaceWrite:
		return p.SandboxPolicy.NetworkAccess
	case SandboxModeExternalSandbox:
		return p.SandboxPolicy.ExternalNetwork.IsEnabled()
	default:
		return false
	}
}
