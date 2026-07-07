package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type AskForApproval string

const (
	ApprovalUnlessTrusted AskForApproval = "untrusted"
	ApprovalOnRequest     AskForApproval = "on-request"
	ApprovalGranular      AskForApproval = "granular"
	ApprovalNever         AskForApproval = "never"
)

type GranularApprovalConfig struct {
	SandboxApproval    bool
	Rules              bool
	SkillApproval      bool
	RequestPermissions bool
	MCPElicitations    bool
}

func (c *GranularApprovalConfig) AllowsSandboxApproval() bool {
	return c != nil && c.SandboxApproval
}

func (c *GranularApprovalConfig) AllowsRulesApproval() bool {
	return c != nil && c.Rules
}

func (c *GranularApprovalConfig) AllowsSkillApproval() bool {
	return c != nil && c.SkillApproval
}

func (c *GranularApprovalConfig) AllowsRequestPermissions() bool {
	return c != nil && c.RequestPermissions
}

func (c *GranularApprovalConfig) AllowsMCPElicitations() bool {
	return c != nil && c.MCPElicitations
}

type NetworkAccess string

const (
	NetworkRestricted NetworkAccess = "restricted"
	NetworkEnabled    NetworkAccess = "enabled"
)

func (n *NetworkAccess) IsEnabled() bool {
	return n != nil && *n == NetworkEnabled
}

type SandboxMode string

const (
	SandboxReadOnly         SandboxMode = "read-only"
	SandboxWorkspaceWrite   SandboxMode = "workspace-write"
	SandboxDangerFullAccess SandboxMode = "danger-full-access"
)

func ParseSandboxMode(value string) (SandboxMode, error) {
	switch strings.TrimSpace(value) {
	case string(SandboxReadOnly):
		return SandboxReadOnly, nil
	case string(SandboxWorkspaceWrite):
		return SandboxWorkspaceWrite, nil
	case string(SandboxDangerFullAccess):
		return SandboxDangerFullAccess, nil
	default:
		return "", fmt.Errorf("unknown sandbox mode %q", value)
	}
}

type SandboxPolicy struct {
	Kind                SandboxMode
	ExternalNetwork     NetworkAccess
	WritableRoots       []string
	NetworkAccess       bool
	ExcludeTmpdirEnvVar bool
	ExcludeSlashTmp     bool
}

func (p *SandboxPolicy) UnmarshalJSON(data []byte) error {
	var raw struct {
		Type                string          `json:"type"`
		NetworkAccess       any             `json:"networkAccess"`
		WritableRoots       []string        `json:"writableRoots"`
		ExcludeTmpdirEnvVar bool            `json:"excludeTmpdirEnvVar"`
		ExcludeSlashTmp     bool            `json:"excludeSlashTmp"`
		Access              json.RawMessage `json:"access"`
		ReadOnlyAccess      json.RawMessage `json:"readOnlyAccess"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch strings.TrimSpace(raw.Type) {
	case "dangerFullAccess", string(SandboxDangerFullAccess):
		*p = *NewDangerFullAccessPolicy()
		return nil
	case "readOnly", string(SandboxReadOnly):
		if legacyRestrictedAccess(raw.Access) {
			return fmt.Errorf("readOnly.access is no longer supported; use permissionProfile for restricted reads")
		}
		*p = SandboxPolicy{
			Kind:          SandboxReadOnly,
			NetworkAccess: sandboxPolicyBool(raw.NetworkAccess),
		}
		return nil
	case "externalSandbox", "external-sandbox":
		*p = SandboxPolicy{
			Kind:            "external-sandbox",
			ExternalNetwork: sandboxPolicyNetworkAccess(raw.NetworkAccess),
		}
		return nil
	case "workspaceWrite", string(SandboxWorkspaceWrite), "":
		if legacyRestrictedAccess(raw.ReadOnlyAccess) {
			return fmt.Errorf("workspaceWrite.readOnlyAccess is no longer supported; use permissionProfile for restricted reads")
		}
		*p = SandboxPolicy{
			Kind:                SandboxWorkspaceWrite,
			WritableRoots:       append([]string(nil), raw.WritableRoots...),
			NetworkAccess:       sandboxPolicyBool(raw.NetworkAccess),
			ExcludeTmpdirEnvVar: raw.ExcludeTmpdirEnvVar,
			ExcludeSlashTmp:     raw.ExcludeSlashTmp,
		}
		return nil
	default:
		return fmt.Errorf("unknown sandbox policy type %q", raw.Type)
	}
}

func (p *SandboxPolicy) MarshalJSON() ([]byte, error) {
	switch p.Kind {
	case SandboxDangerFullAccess:
		return json.Marshal(struct {
			Type string `json:"type"`
		}{Type: "dangerFullAccess"})
	case SandboxReadOnly:
		return json.Marshal(struct {
			Type          string `json:"type"`
			NetworkAccess bool   `json:"networkAccess"`
		}{
			Type:          "readOnly",
			NetworkAccess: p.NetworkAccess,
		})
	case "external-sandbox":
		networkAccess := p.ExternalNetwork
		if networkAccess == "" {
			networkAccess = NetworkRestricted
		}
		return json.Marshal(struct {
			Type          string        `json:"type"`
			NetworkAccess NetworkAccess `json:"networkAccess"`
		}{
			Type:          "externalSandbox",
			NetworkAccess: networkAccess,
		})
	case SandboxWorkspaceWrite, "":
		writableRoots := append([]string(nil), p.WritableRoots...)
		if writableRoots == nil {
			writableRoots = []string{}
		}
		return json.Marshal(struct {
			Type                string   `json:"type"`
			WritableRoots       []string `json:"writableRoots"`
			NetworkAccess       bool     `json:"networkAccess"`
			ExcludeTmpdirEnvVar bool     `json:"excludeTmpdirEnvVar"`
			ExcludeSlashTmp     bool     `json:"excludeSlashTmp"`
		}{
			Type:                "workspaceWrite",
			WritableRoots:       writableRoots,
			NetworkAccess:       p.NetworkAccess,
			ExcludeTmpdirEnvVar: p.ExcludeTmpdirEnvVar,
			ExcludeSlashTmp:     p.ExcludeSlashTmp,
		})
	default:
		type sandboxPolicyAlias SandboxPolicy
		return json.Marshal(sandboxPolicyAlias(*p))
	}
}

type WritableRoot struct {
	Root                   string
	ReadOnlySubpaths       []string
	ProtectedMetadataNames []string
}

func NewReadOnlyPolicy() *SandboxPolicy {
	return &SandboxPolicy{Kind: SandboxReadOnly}
}

func NewWorkspaceWritePolicy() *SandboxPolicy {
	return &SandboxPolicy{
		Kind:                SandboxWorkspaceWrite,
		ExcludeTmpdirEnvVar: false,
		ExcludeSlashTmp:     false,
	}
}

func NewDangerFullAccessPolicy() *SandboxPolicy {
	return &SandboxPolicy{Kind: SandboxDangerFullAccess, NetworkAccess: true}
}

func NewExternalSandboxPolicy(network NetworkAccess) *SandboxPolicy {
	return &SandboxPolicy{Kind: "external-sandbox", ExternalNetwork: network}
}

func SandboxPolicyFromMode(mode SandboxMode) (*SandboxPolicy, error) {
	switch mode {
	case SandboxReadOnly:
		return NewReadOnlyPolicy(), nil
	case SandboxWorkspaceWrite:
		return NewWorkspaceWritePolicy(), nil
	case SandboxDangerFullAccess:
		return NewDangerFullAccessPolicy(), nil
	default:
		return nil, fmt.Errorf("unknown sandbox mode %q", mode)
	}
}

func sandboxPolicyBool(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "enabled") || strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func sandboxPolicyNetworkAccess(value any) NetworkAccess {
	switch typed := value.(type) {
	case string:
		switch NetworkAccess(strings.TrimSpace(typed)) {
		case NetworkEnabled:
			return NetworkEnabled
		default:
			return NetworkRestricted
		}
	case bool:
		if typed {
			return NetworkEnabled
		}
		return NetworkRestricted
	default:
		return NetworkRestricted
	}
}

func legacyRestrictedAccess(data json.RawMessage) bool {
	if len(data) == 0 || string(data) == "null" {
		return false
	}
	var raw struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(raw.Type), "restricted")
}

func (p *SandboxPolicy) HasFullDiskReadAccess() bool {
	return true
}

func (p *SandboxPolicy) HasFullDiskWriteAccess() bool {
	if p == nil {
		return false
	}
	return p.Kind == SandboxDangerFullAccess || p.Kind == "external-sandbox"
}

func (p *SandboxPolicy) HasFullNetworkAccess() bool {
	if p == nil {
		return false
	}
	switch p.Kind {
	case SandboxDangerFullAccess:
		return true
	case "external-sandbox":
		return (&p.ExternalNetwork).IsEnabled()
	case SandboxReadOnly, SandboxWorkspaceWrite:
		return p.NetworkAccess
	default:
		return false
	}
}

func (p *SandboxPolicy) GetWritableRootsWithCWD(cwd string) []WritableRoot {
	if p == nil || p.Kind != SandboxWorkspaceWrite {
		return nil
	}
	var roots []string
	roots = append(roots, p.WritableRoots...)
	if cwd != "" {
		roots = append(roots, cwd)
	}
	if !p.ExcludeSlashTmp {
		roots = append(roots, slashTmpPath())
	}
	if !p.ExcludeTmpdirEnvVar {
		if tmpdir := strings.TrimSpace(os.Getenv("TMPDIR")); tmpdir != "" {
			roots = append(roots, tmpdir)
		}
	}
	return buildWritableRoots(roots)
}

func (r *WritableRoot) IsPathWritable(path string) bool {
	if r == nil {
		return false
	}
	root := cleanAbs(r.Root)
	target := cleanAbs(path)
	if !sameOrWithin(target, root) {
		return false
	}
	for _, subpath := range r.ReadOnlySubpaths {
		if sameOrWithin(target, cleanAbs(subpath)) {
			return false
		}
	}
	if r.PathContainsProtectedMetadataName(target) {
		return false
	}
	return true
}

func (r *WritableRoot) PathContainsProtectedMetadataName(path string) bool {
	if r == nil {
		return false
	}
	root := cleanAbs(r.Root)
	target := cleanAbs(path)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	first := rel
	if idx := strings.IndexAny(rel, `/\`); idx >= 0 {
		first = rel[:idx]
	}
	for _, name := range r.ProtectedMetadataNames {
		if first == name {
			return true
		}
	}
	return false
}

func buildWritableRoots(paths []string) []WritableRoot {
	seen := map[string]bool{}
	var out []WritableRoot
	for _, path := range paths {
		path = cleanAbs(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, WritableRoot{
			Root:                   path,
			ReadOnlySubpaths:       protectedSubpaths(path),
			ProtectedMetadataNames: []string{".git", ".codex"},
		})
	}
	return out
}

func protectedSubpaths(root string) []string {
	return []string{
		filepath.Join(root, ".git"),
		filepath.Join(root, ".codex"),
	}
}

func cleanAbs(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func sameOrWithin(path, root string) bool {
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func slashTmpPath() string {
	if filepath.Separator == '/' {
		return "/tmp"
	}
	return os.TempDir()
}

func requireNonEmpty(value, name string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New(name + " must not be empty")
	}
	return nil
}
