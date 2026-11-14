package sandbox

import "path/filepath"

// LegacySandboxPolicyLabel classifies a canonical profile without rebuilding
// the legacy policy. It mirrors Rust PermissionProfile::legacy_sandbox_policy_label.
func (p *PermissionProfile) LegacySandboxPolicyLabel(cwd string) string {
	if p == nil {
		return "read-only"
	}
	wire := enforcementPermissionWire(p)
	switch wire.Type {
	case "disabled":
		return "danger-full-access"
	case "external":
		return "external-sandbox"
	}
	if wire.FileSys == nil {
		return "read-only"
	}
	if wire.FileSys.hasFullDiskWriteAccess() {
		if wire.Network == string(NetworkEnabled) {
			return "danger-full-access"
		}
		return "external-sandbox"
	}
	for _, entry := range wire.FileSys.Entries {
		if entry.Access != string(FileSystemAccessWrite) {
			continue
		}
		switch entry.Path.Type {
		case "path":
			return "workspace-write"
		case "special":
			switch entry.Path.Value.Kind {
			case "root", "tmpdir":
				return "workspace-write"
			case "project_roots":
				if entry.Path.Value.Subpath == nil || filepath.IsAbs(cwd) {
					return "workspace-write"
				}
			case "slash_tmp":
				if supportsSymbolicSlashTmp() {
					return "workspace-write"
				}
			}
		}
	}
	return "read-only"
}
