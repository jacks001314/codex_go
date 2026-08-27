package windowssandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	coresandbox "codex_go/sandbox"
)

type WindowsSandboxTokenMode string

const (
	WindowsSandboxTokenModeReadOnlyCapability      WindowsSandboxTokenMode = "readOnlyCapability"
	WindowsSandboxTokenModeWritableRootsCapability WindowsSandboxTokenMode = "writableRootsCapability"
	WindowsSandboxTokenModeReadOnly                WindowsSandboxTokenMode = WindowsSandboxTokenModeReadOnlyCapability
	WindowsSandboxTokenModeWorkspaceWrite          WindowsSandboxTokenMode = WindowsSandboxTokenModeWritableRootsCapability
	WindowsSandboxTokenModeDisabled                WindowsSandboxTokenMode = "disabled"
)

type WindowsWritableRoot struct {
	Root             string
	ReadOnlySubpaths []string
}

type ResolvedWindowsSandboxPermissions struct {
	ProfileID      string
	FileSystem     *coresandbox.SandboxPolicy
	NetworkEnabled bool
	WorkspaceRoots []string
}

func TokenModeForPermissionProfile(profile *coresandbox.PermissionProfile, workspaceRoots []string, cwd string, envMap map[string]string) (WindowsSandboxTokenMode, error) {
	permissions, err := ResolvePermissions(profile, workspaceRoots)
	if err != nil {
		return "", err
	}
	if permissions.FileSystem.HasFullDiskWriteAccess() {
		return "", fmt.Errorf("permission profile requests full-disk filesystem writes, which cannot be enforced by the Windows sandbox")
	}
	if len(permissions.WritableRootsForCWD(cwd, envMap)) == 0 {
		return WindowsSandboxTokenModeReadOnlyCapability, nil
	}
	return WindowsSandboxTokenModeWritableRootsCapability, nil
}

func TokenModeForPermissionProfileID(profileID string, workspaceRoots []string, cwd string, envMap map[string]string) (WindowsSandboxTokenMode, error) {
	profile, _, err := coresandbox.ResolvePermissionProfile(profileID)
	if err != nil {
		return "", err
	}
	return TokenModeForPermissionProfile(profile, workspaceRoots, cwd, envMap)
}

func ResolvePermissions(profile *coresandbox.PermissionProfile, workspaceRoots []string) (*ResolvedWindowsSandboxPermissions, error) {
	if profile == nil || profile.Disabled || profile.SandboxPolicy == nil {
		return nil, fmt.Errorf("only managed permission profiles can be enforced by the Windows sandbox")
	}
	switch profile.SandboxPolicy.Kind {
	case coresandbox.SandboxReadOnly, coresandbox.SandboxWorkspaceWrite:
	default:
		return nil, fmt.Errorf("only restricted managed filesystem permissions can be enforced by the Windows sandbox")
	}
	return &ResolvedWindowsSandboxPermissions{
		FileSystem:     profile.SandboxPolicy,
		NetworkEnabled: profile.AllowsNetwork(),
		WorkspaceRoots: cloneStrings(workspaceRoots),
	}, nil
}

func (p *ResolvedWindowsSandboxPermissions) ShouldApplyNetworkBlock() bool {
	return p != nil && !p.NetworkEnabled
}

func (p *ResolvedWindowsSandboxPermissions) NetworkPolicy() string {
	if p == nil || !p.NetworkEnabled {
		return string(coresandbox.NetworkRestricted)
	}
	return string(coresandbox.NetworkEnabled)
}

func (p *ResolvedWindowsSandboxPermissions) IsEnforceableByWindowsSandbox() bool {
	if p == nil || p.FileSystem == nil {
		return false
	}
	return p.FileSystem.Kind == coresandbox.SandboxReadOnly || p.FileSystem.Kind == coresandbox.SandboxWorkspaceWrite
}

func (p *ResolvedWindowsSandboxPermissions) HasFullDiskReadAccess() bool {
	if p == nil || p.FileSystem == nil {
		return false
	}
	return p.FileSystem.HasFullDiskReadAccess()
}

func (p *ResolvedWindowsSandboxPermissions) IncludePlatformDefaults() bool {
	return p != nil && p.FileSystem != nil
}

func (p *ResolvedWindowsSandboxPermissions) ReadableRootsForCWD(cwd string) []string {
	if p == nil || p.FileSystem == nil || p.FileSystem.Kind != coresandbox.SandboxReadOnly {
		return nil
	}
	return []string{cleanWindowsSandboxAbs(cwd)}
}

// HasSymbolicRootReadAccess reports whether the read-only sandbox policy grants
// a readable symbolic filesystem root for cwd (a `:root` read entry), distinct
// from full-disk read access, so narrower deny-read rules do not disable the
// broad-read setup (Rust #40441 has_symbolic_root_read_access).
func (p *ResolvedWindowsSandboxPermissions) HasSymbolicRootReadAccess(cwd string) bool {
	if p == nil || p.FileSystem == nil || p.FileSystem.Kind != coresandbox.SandboxReadOnly {
		return false
	}
	return strings.TrimSpace(cwd) != ""
}

func (p *ResolvedWindowsSandboxPermissions) UsesWriteCapabilitiesForCWD(cwd string, envMap map[string]string) bool {
	return len(p.WritableRootsForCWD(cwd, envMap)) > 0
}

func (p *ResolvedWindowsSandboxPermissions) WritableRootsForCWD(cwd string, envMap map[string]string) []WindowsWritableRoot {
	if p == nil || p.FileSystem == nil || p.FileSystem.Kind != coresandbox.SandboxWorkspaceWrite {
		return nil
	}
	var roots []string
	roots = append(roots, p.FileSystem.WritableRoots...)
	if cwd != "" {
		roots = append(roots, cwd)
	}
	if !p.FileSystem.ExcludeTmpdirEnvVar {
		roots = append(roots, windowsTempEnvRoots(envMap)...)
	}
	return buildWindowsWritableRoots(roots)
}

func windowsTempEnvRoots(envMap map[string]string) []string {
	var roots []string
	seen := map[string]bool{}
	for _, key := range []string{"TEMP", "TMP"} {
		value := strings.TrimSpace(envMap[key])
		if value == "" {
			value = strings.TrimSpace(os.Getenv(key))
		}
		if value == "" || !isWindowsSandboxAbs(value) {
			continue
		}
		cleaned := cleanWindowsSandboxAbs(value)
		if cleaned == "" || seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		roots = append(roots, cleaned)
	}
	return roots
}

func buildWindowsWritableRoots(paths []string) []WindowsWritableRoot {
	seen := map[string]bool{}
	var out []WindowsWritableRoot
	for _, path := range paths {
		cleaned := cleanWindowsSandboxAbs(path)
		if cleaned == "" || seen[cleaned] {
			continue
		}
		seen[cleaned] = true
		out = append(out, WindowsWritableRoot{
			Root: cleaned,
			ReadOnlySubpaths: []string{
				filepath.Join(cleaned, ".git"),
				filepath.Join(cleaned, ".gcode"),
			},
		})
	}
	return out
}

func cleanWindowsSandboxAbs(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func isWindowsSandboxAbs(path string) bool {
	if filepath.IsAbs(path) {
		return true
	}
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}
