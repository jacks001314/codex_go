package sandbox

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

const (
	BuiltInPermissionProfileReadOnly         = "read-only"
	BuiltInPermissionProfileWorkspace        = "workspace"
	BuiltInPermissionProfileDangerFullAccess = "danger-full-access"
)

type ActivePermissionProfile struct {
	ID      string
	Extends string
}

func (p *ActivePermissionProfile) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID      string  `json:"id"`
		Extends *string `json:"extends"`
	}{
		ID:      p.ID,
		Extends: stringPtrIfNotEmpty(p.Extends),
	})
}

type PermissionProfile struct {
	Disabled          bool
	SandboxPolicy     *SandboxPolicy
	NetworkEnabled    bool
	DeniedReadEntries []FileSystemSandboxEntry `json:"denyReadEntries,omitempty"`

	runtimeJSON string
}

type AdditionalPermissionProfile struct {
	Network    *bool
	FileSystem []string
}

func (p *AdditionalPermissionProfile) UnmarshalJSON(data []byte) error {
	var raw struct {
		Network *struct {
			Enabled *bool `json:"enabled"`
		} `json:"network"`
		FileSystem *struct {
			Read  []string `json:"read"`
			Write []string `json:"write"`
		} `json:"fileSystem"`
		FileSystemSnake *struct {
			Read  []string `json:"read"`
			Write []string `json:"write"`
		} `json:"file_system"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Network = nil
	if raw.Network != nil && raw.Network.Enabled != nil {
		value := *raw.Network.Enabled
		p.Network = &value
	}
	p.FileSystem = nil
	if raw.FileSystem != nil {
		p.FileSystem = append(p.FileSystem, raw.FileSystem.Read...)
		p.FileSystem = append(p.FileSystem, raw.FileSystem.Write...)
	}
	if raw.FileSystemSnake != nil {
		p.FileSystem = append(p.FileSystem, raw.FileSystemSnake.Read...)
		p.FileSystem = append(p.FileSystem, raw.FileSystemSnake.Write...)
	}
	return nil
}

func (p *AdditionalPermissionProfile) MarshalJSON() ([]byte, error) {
	var network *AdditionalNetworkPermissions
	if p.Network != nil {
		network = &AdditionalNetworkPermissions{Enabled: p.Network}
	}
	var fileSystem *AdditionalFileSystemPermissions
	if p.FileSystem != nil {
		fileSystem = &AdditionalFileSystemPermissions{Read: nil, Write: append([]string(nil), p.FileSystem...)}
	}
	return json.Marshal(struct {
		Network    *AdditionalNetworkPermissions    `json:"network"`
		FileSystem *AdditionalFileSystemPermissions `json:"fileSystem"`
	}{
		Network:    network,
		FileSystem: fileSystem,
	})
}

type ApprovalPreset struct {
	ID                      string
	Label                   string
	Description             string
	Approval                AskForApproval
	ActivePermissionProfile ActivePermissionProfile
	PermissionProfile       PermissionProfile
}

type SandboxPermissions string

const (
	SandboxPermissionsUseDefault                SandboxPermissions = "use_default"
	SandboxPermissionsRequireEscalated          SandboxPermissions = "require_escalated"
	SandboxPermissionsWithAdditionalPermissions SandboxPermissions = "with_additional_permissions"
)

func ReadOnlyPermissionProfile() PermissionProfile {
	return PermissionProfile{SandboxPolicy: NewReadOnlyPolicy()}
}

func WorkspaceWritePermissionProfile() PermissionProfile {
	return PermissionProfile{SandboxPolicy: NewWorkspaceWritePolicy()}
}

func FullAccessPermissionProfile() PermissionProfile {
	return PermissionProfile{Disabled: true, SandboxPolicy: NewDangerFullAccessPolicy(), NetworkEnabled: true}
}

func BuiltinApprovalPresets() []ApprovalPreset {
	return []ApprovalPreset{
		{
			ID:          "read-only",
			Label:       "Read Only",
			Description: "Codex can read files in the current workspace. Approval is required to edit files or access the internet.",
			Approval:    ApprovalOnRequest,
			ActivePermissionProfile: ActivePermissionProfile{
				ID: BuiltInPermissionProfileReadOnly,
			},
			PermissionProfile: ReadOnlyPermissionProfile(),
		},
		{
			ID:          "auto",
			Label:       "Default",
			Description: "Codex can read and edit files in the current workspace, and run commands. Approval is required to access the internet or edit other files.",
			Approval:    ApprovalOnRequest,
			ActivePermissionProfile: ActivePermissionProfile{
				ID: BuiltInPermissionProfileWorkspace,
			},
			PermissionProfile: WorkspaceWritePermissionProfile(),
		},
		{
			ID:          "full-access",
			Label:       "Full Access",
			Description: "Codex can edit files outside this workspace and access the internet without asking for approval.",
			Approval:    ApprovalNever,
			ActivePermissionProfile: ActivePermissionProfile{
				ID: BuiltInPermissionProfileDangerFullAccess,
			},
			PermissionProfile: FullAccessPermissionProfile(),
		},
	}
}

func BuiltinPermissionProfileForActivePermissionProfile(active *ActivePermissionProfile) (*PermissionProfile, bool) {
	if active == nil || active.Extends != "" {
		return nil, false
	}
	var profile PermissionProfile
	switch active.ID {
	case BuiltInPermissionProfileReadOnly:
		profile = ReadOnlyPermissionProfile()
	case BuiltInPermissionProfileWorkspace:
		profile = WorkspaceWritePermissionProfile()
	case BuiltInPermissionProfileDangerFullAccess:
		profile = FullAccessPermissionProfile()
	default:
		return nil, false
	}
	return &profile, true
}

func (p *PermissionProfile) LegacySandboxPolicy() *SandboxPolicy {
	if p == nil {
		return NewReadOnlyPolicy()
	}
	if p.Disabled {
		return NewDangerFullAccessPolicy()
	}
	if p.SandboxPolicy == nil {
		return NewReadOnlyPolicy()
	}
	return p.SandboxPolicy
}

func (p *PermissionProfile) AllowsNetwork() bool {
	if p == nil {
		return false
	}
	if p.Disabled || p.NetworkEnabled {
		return true
	}
	return p.SandboxPolicy != nil && p.SandboxPolicy.HasFullNetworkAccess()
}

func (p *PermissionProfile) HasDenyReadEntries() bool {
	if p == nil {
		return false
	}
	for _, entry := range p.DeniedReadEntries {
		if !supportsSymbolicSlashTmp() && isSymbolicSlashTmpPath(entry.Path) {
			continue
		}
		return true
	}
	return false
}

func UnsandboxedExecutionAllowed(profile *PermissionProfile) bool {
	return profile == nil || !profile.HasDenyReadEntries()
}

func SandboxPermissionsPreservingDeniedReads(permissions SandboxPermissions, profile *PermissionProfile) SandboxPermissions {
	if permissions == SandboxPermissionsRequireEscalated && !UnsandboxedExecutionAllowed(profile) {
		return SandboxPermissionsUseDefault
	}
	return permissions
}

func (p *SandboxPermissions) UsesAdditionalPermissions() bool {
	return p != nil && *p == SandboxPermissionsWithAdditionalPermissions
}

func NormalizeAndValidateAdditionalPermissions(
	additionalPermissionsAllowed bool,
	approvalPolicy AskForApproval,
	sandboxPermissions SandboxPermissions,
	additionalPermissions *AdditionalPermissionProfile,
	permissionsPreapproved bool,
	cwd string,
) (*AdditionalPermissionProfile, error) {
	usesAdditionalPermissions := sandboxPermissions == SandboxPermissionsWithAdditionalPermissions
	if !permissionsPreapproved && !additionalPermissionsAllowed && (usesAdditionalPermissions || additionalPermissions != nil) {
		return nil, fmt.Errorf("additional permissions are disabled; enable exec_permission_approvals before using with_additional_permissions")
	}
	if usesAdditionalPermissions {
		if !permissionsPreapproved && approvalPolicy != ApprovalOnRequest {
			return nil, fmt.Errorf("approval policy is %s; reject command - you cannot request additional permissions unless the approval policy is on-request", approvalPolicy)
		}
		if additionalPermissions == nil {
			return nil, fmt.Errorf("missing additional_permissions; provide at least one of network or file_system when using with_additional_permissions")
		}
		normalized, err := NormalizeAdditionalPermissions(*additionalPermissions, cwd)
		if err != nil {
			return nil, err
		}
		if normalized.IsEmpty() {
			return nil, fmt.Errorf("additional_permissions must include at least one requested permission in network or file_system")
		}
		return &normalized, nil
	}
	if additionalPermissions != nil {
		return nil, fmt.Errorf("additional_permissions requires sandbox_permissions set to with_additional_permissions")
	}
	return nil, nil
}

func NormalizeAdditionalPermissions(profile AdditionalPermissionProfile, cwd string) (AdditionalPermissionProfile, error) {
	out := AdditionalPermissionProfile{Network: profile.Network}
	seen := map[string]bool{}
	for _, path := range profile.FileSystem {
		if err := requireNonEmpty(path, "file_system permission path"); err != nil {
			return AdditionalPermissionProfile{}, err
		}
		if !filepath.IsAbs(path) && cwd != "" {
			path = filepath.Join(cwd, path)
		}
		path = cleanAbs(path)
		if !seen[path] {
			seen[path] = true
			out.FileSystem = append(out.FileSystem, path)
		}
	}
	return out, nil
}

func (p *AdditionalPermissionProfile) IsEmpty() bool {
	return p == nil || (p.Network == nil && len(p.FileSystem) == 0)
}

func MergePermissionProfiles(left, right *AdditionalPermissionProfile) *AdditionalPermissionProfile {
	if left == nil && right == nil {
		return nil
	}
	network := (*bool)(nil)
	if left != nil && left.Network != nil {
		value := *left.Network
		network = &value
	}
	if right != nil && right.Network != nil {
		value := *right.Network
		network = &value
	}
	var paths []string
	seen := map[string]bool{}
	appendPaths := func(profile *AdditionalPermissionProfile) {
		if profile == nil {
			return
		}
		for _, path := range profile.FileSystem {
			cleaned := cleanAbs(path)
			if cleaned != "" && !seen[cleaned] {
				seen[cleaned] = true
				paths = append(paths, cleaned)
			}
		}
	}
	appendPaths(left)
	appendPaths(right)
	return &AdditionalPermissionProfile{Network: network, FileSystem: paths}
}
