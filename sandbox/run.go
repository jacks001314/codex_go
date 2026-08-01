package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"codex_go/sandbox/linuxsandbox"

	json "github.com/goccy/go-json"
)

var (
	ErrInvalidSandboxRunRequest   = errors.New("invalid sandbox run request")
	ErrPlatformSandboxUnsupported = errors.New("platform sandbox unsupported")
)

type CommandRunRequest struct {
	PermissionProfile             string
	ResolvedPermissionProfile     *PermissionProfile
	ResolvedPermissionProfileID   string
	ResolvedPermissionProfileJSON string
	ConfigProfile                 string
	CWD                           string
	IncludeManagedConfig          bool
	SandboxStateJSON              string
	SandboxReadableRoots          []string
	SandboxDisableNetwork         bool
	AllowUnixSockets              []string
	LogDenials                    bool
	UseLegacyLandlock             bool
	AllowNetworkForProxy          bool
	CodexLinuxSandboxExe          string
	ConfigOverrides               []string
	Command                       []string
}

type CommandRunPlan struct {
	Command                 []string
	CWD                     string
	SandboxType             SandboxType
	PermissionProfileID     string
	PermissionProfile       *PermissionProfile
	PermissionProfileJSON   string
	CodexLinuxSandboxExe    string
	UseLegacyLandlock       bool
	AllowNetworkForProxy    bool
	RequiresPlatformSandbox bool
	UnsupportedReason       string
}

type SandboxState struct {
	PermissionProfile    *PermissionProfile `json:"permission_profile"`
	CodexLinuxSandboxExe string             `json:"codex_linux_sandbox_exe"`
	SandboxCWD           string             `json:"sandbox_cwd"`
	UseLegacyLandlock    bool               `json:"use_legacy_landlock"`
	HasUseLegacyLandlock bool               `json:"-"`
	PermissionProfileRaw json.RawMessage    `json:"-"`
}

func (s *SandboxState) UnmarshalJSON(data []byte) error {
	var raw struct {
		PermissionProfile    json.RawMessage `json:"permission_profile"`
		CodexLinuxSandboxExe string          `json:"codex_linux_sandbox_exe"`
		SandboxCWD           string          `json:"sandbox_cwd"`
		UseLegacyLandlock    *bool           `json:"use_legacy_landlock"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = SandboxState{
		CodexLinuxSandboxExe: raw.CodexLinuxSandboxExe,
		SandboxCWD:           raw.SandboxCWD,
		PermissionProfileRaw: raw.PermissionProfile,
	}
	if len(raw.PermissionProfile) > 0 && string(raw.PermissionProfile) != "null" {
		profile, err := parsePermissionProfileJSON(raw.PermissionProfile)
		if err != nil {
			return err
		}
		s.PermissionProfile = profile
	}
	if raw.UseLegacyLandlock != nil {
		s.UseLegacyLandlock = *raw.UseLegacyLandlock
		s.HasUseLegacyLandlock = true
	}
	return nil
}

func BuildCommandRunPlan(req *CommandRunRequest) (*CommandRunPlan, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is nil", ErrInvalidSandboxRunRequest)
	}
	if len(req.Command) == 0 || strings.TrimSpace(req.Command[0]) == "" {
		return nil, fmt.Errorf("%w: command is required", ErrInvalidSandboxRunRequest)
	}
	plan := &CommandRunPlan{
		Command:              cloneSandboxStrings(req.Command),
		CWD:                  strings.TrimSpace(req.CWD),
		SandboxType:          SandboxTypeNone,
		UseLegacyLandlock:    req.UseLegacyLandlock,
		AllowNetworkForProxy: req.AllowNetworkForProxy,
		CodexLinuxSandboxExe: cleanRunPath(req.CodexLinuxSandboxExe),
	}
	var unsupported []string
	var sandboxState *SandboxState
	permissionUnsupportedAdded := false
	if req.ResolvedPermissionProfile != nil {
		canonical, err := CanonicalPermissionProfile(req.ResolvedPermissionProfile, req.ResolvedPermissionProfileJSON)
		if err != nil {
			return nil, fmt.Errorf("%w: resolved permission profile JSON is invalid: %v", ErrInvalidSandboxRunRequest, err)
		}
		profile := clonePermissionProfile(canonical)
		profileID := strings.TrimSpace(req.ResolvedPermissionProfileID)
		if profileID == "" {
			profileID = "resolved"
		}
		plan.PermissionProfileID = profileID
		plan.PermissionProfile = &profile
		profileJSON, err := RuntimePermissionProfileJSON(profile)
		if err != nil {
			return nil, err
		}
		plan.PermissionProfileJSON = profileJSON
		if !profile.Disabled && !platformSupportsPermissionProfileSandbox() {
			unsupported = append(unsupported, fmt.Sprintf("permission profile %q", profileID))
			permissionUnsupportedAdded = true
		}
	} else if strings.TrimSpace(req.ResolvedPermissionProfileJSON) != "" {
		return nil, fmt.Errorf("%w: resolved permission profile JSON requires resolved permission profile metadata", ErrInvalidSandboxRunRequest)
	} else if strings.TrimSpace(req.PermissionProfile) != "" {
		profile, profileID, err := ResolvePermissionProfile(req.PermissionProfile)
		if err != nil {
			return nil, err
		}
		plan.PermissionProfileID = profileID
		plan.PermissionProfile = profile
		if profile == nil || !profile.Disabled {
			if !platformSupportsPermissionProfileSandbox() {
				unsupported = append(unsupported, fmt.Sprintf("permission profile %q", profileID))
				permissionUnsupportedAdded = true
			}
		}
	}
	if strings.TrimSpace(req.ConfigProfile) != "" {
		unsupported = append(unsupported, "config profile")
	}
	if len(req.ConfigOverrides) > 0 {
		unsupported = append(unsupported, "config overrides")
	}
	if strings.TrimSpace(req.SandboxStateJSON) != "" {
		parsed, err := parseSandboxStateJSON(req.SandboxStateJSON)
		if err != nil {
			return nil, err
		}
		sandboxState = parsed
		plan.UseLegacyLandlock = sandboxState.UseLegacyLandlock
		if sandboxState.PermissionProfile != nil {
			profile := clonePermissionProfile(sandboxState.PermissionProfile)
			if isRustPermissionProfileType(sandboxState.PermissionProfileRaw, "external") {
				profile = ReadOnlyPermissionProfile()
				plan.PermissionProfileJSON = mustRustPermissionProfileJSON(profile)
			} else {
				plan.PermissionProfileJSON = string(sandboxState.PermissionProfileRaw)
			}
			plan.PermissionProfile = &profile
			plan.PermissionProfileID = "sandbox-state"
		}
		if strings.TrimSpace(sandboxState.SandboxCWD) != "" {
			plan.CWD = cleanRunPath(sandboxState.SandboxCWD)
		}
		if strings.TrimSpace(sandboxState.CodexLinuxSandboxExe) != "" {
			plan.CodexLinuxSandboxExe = cleanRunPath(sandboxState.CodexLinuxSandboxExe)
		}
	}
	if err := applySandboxStateOverrides(plan, req); err != nil {
		return nil, err
	}
	if !platformSupportsPermissionProfileSandbox() && plan.PermissionProfile != nil && !plan.PermissionProfile.Disabled && !permissionUnsupportedAdded {
		unsupported = append(unsupported, "sandbox state")
	}
	if len(req.AllowUnixSockets) > 0 && runtime.GOOS != "darwin" {
		unsupported = append(unsupported, "unix socket allowlist")
	}
	if req.LogDenials {
		if runtime.GOOS != "darwin" {
			unsupported = append(unsupported, "denial logging")
		}
	}
	if len(unsupported) > 0 {
		plan.RequiresPlatformSandbox = true
		plan.UnsupportedReason = strings.Join(unsupported, ", ")
	} else if runtime.GOOS == "linux" && plan.PermissionProfile != nil && !plan.PermissionProfile.Disabled {
		profileJSON, err := linuxPermissionProfileJSONForPlan(plan)
		if err != nil {
			return nil, err
		}
		wrapped, err := CreateLinuxSandboxCommandArgsForPermissionProfileJSONWithSandboxExe(
			plan.CodexLinuxSandboxExe,
			plan.Command,
			plan.CWD,
			profileJSON,
			plan.CWD,
			plan.UseLegacyLandlock,
			plan.AllowNetworkForProxy,
		)
		if err != nil {
			return nil, err
		}
		plan.Command = wrapped
		plan.SandboxType = SandboxTypeLinuxSeccomp
	} else if runtime.GOOS == "darwin" && plan.PermissionProfile != nil && !plan.PermissionProfile.Disabled {
		wrapped, err := createSeatbeltCommandArgs(plan.Command, plan.CWD, plan.PermissionProfile, req.AllowUnixSockets)
		if err != nil {
			return nil, err
		}
		plan.Command = wrapped
		plan.SandboxType = SandboxTypeMacosSeatbelt
	} else if runtime.GOOS == "windows" && plan.PermissionProfile != nil && !plan.PermissionProfile.Disabled {
		plan.SandboxType = SandboxTypeWindowsRestrictedToken
	}
	return plan, nil
}

func platformSupportsPermissionProfileSandbox() bool {
	return runtime.GOOS == "linux" || runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

func parseSandboxStateJSON(raw string) (*SandboxState, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var state SandboxState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return nil, fmt.Errorf("%w: sandbox state JSON is invalid: %v", ErrInvalidSandboxRunRequest, err)
	}
	if state.PermissionProfile == nil {
		return nil, fmt.Errorf("%w: sandbox state JSON missing permission_profile", ErrInvalidSandboxRunRequest)
	}
	if strings.TrimSpace(state.SandboxCWD) == "" {
		return nil, fmt.Errorf("%w: sandbox state JSON missing sandbox_cwd", ErrInvalidSandboxRunRequest)
	}
	if !filepath.IsAbs(state.SandboxCWD) {
		return nil, fmt.Errorf("%w: sandbox state cwd is not native to this host", ErrInvalidSandboxRunRequest)
	}
	return &state, nil
}

func applySandboxStateOverrides(plan *CommandRunPlan, req *CommandRunRequest) error {
	if len(req.SandboxReadableRoots) == 0 && !req.SandboxDisableNetwork {
		return nil
	}
	if plan.PermissionProfile == nil {
		profile := ReadOnlyPermissionProfile()
		plan.PermissionProfile = &profile
		if plan.PermissionProfileID == "" {
			plan.PermissionProfileID = "sandbox-state"
		}
	}
	if plan.PermissionProfile.Disabled && req.SandboxDisableNetwork {
		return fmt.Errorf("%w: --sandbox-state-disable-network cannot be applied to a disabled permission profile", ErrInvalidSandboxRunRequest)
	}
	if plan.PermissionProfile.Disabled {
		return nil
	}
	profile := clonePermissionProfile(plan.PermissionProfile)
	profileJSON, err := linuxPermissionProfileJSONForPlan(plan)
	if err != nil {
		return err
	}
	rustProfile, err := parseRustPermissionProfileWireJSON([]byte(profileJSON))
	if err != nil {
		return err
	}
	if len(req.SandboxReadableRoots) > 0 {
		rustProfile.addReadableRoots(plan.CWD, req.SandboxReadableRoots)
	}
	if req.SandboxDisableNetwork {
		profile.NetworkEnabled = false
		if profile.SandboxPolicy != nil {
			profile.SandboxPolicy.NetworkAccess = false
			if profile.SandboxPolicy.Kind == "external-sandbox" {
				profile.SandboxPolicy.ExternalNetwork = NetworkRestricted
			}
		}
		rustProfile.Network = string(NetworkRestricted)
	}
	data, err := json.Marshal(rustProfile)
	if err != nil {
		return err
	}
	profile, err = canonicalPermissionProfileFromJSON(data)
	if err != nil {
		return err
	}
	plan.PermissionProfileJSON = profile.runtimeJSON
	plan.PermissionProfile = &profile
	return nil
}

func (r *rustPermissionProfileWire) addReadableRoots(cwd string, roots []string) {
	if r == nil || r.Type != "managed" {
		return
	}
	if r.FileSys == nil || r.FileSys.Type == "unrestricted" {
		return
	}
	if r.FileSys.Type == "" {
		r.FileSys.Type = "restricted"
	}
	for _, root := range roots {
		cleaned := cleanRunPathWithCWD(root, cwd)
		if cleaned == "" || r.canReadPathWithCWD(cleaned, cwd) {
			continue
		}
		r.FileSys.Entries = append(r.FileSys.Entries, rustPermissionFilesystemEntry{
			Path:   rustPermissionFilesystemPath{Type: "path", Path: cleaned},
			Access: string(FileSystemAccessRead),
		})
	}
}

func (r *rustPermissionProfileWire) canReadPathWithCWD(path string, cwd string) bool {
	if r == nil || r.FileSys == nil {
		return false
	}
	if r.FileSys.Type == "unrestricted" {
		return true
	}
	target := cleanRunPathWithCWD(path, cwd)
	if target == "" {
		return false
	}
	var bestAccess string
	bestSpecificity := -1
	bestPrecedence := -1
	for _, entry := range r.FileSys.Entries {
		entryPath := entry.Path.resolvedRuntimePath(cwd)
		if entryPath == "" || !sameOrWithin(target, entryPath) {
			continue
		}
		specificity := pathComponentCount(entryPath)
		precedence := runtimeAccessPrecedence(entry.Access)
		if specificity > bestSpecificity || (specificity == bestSpecificity && precedence > bestPrecedence) {
			bestAccess = entry.Access
			bestSpecificity = specificity
			bestPrecedence = precedence
		}
	}
	return runtimeAccessCanRead(bestAccess)
}

func parsePermissionProfileJSON(raw json.RawMessage) (*PermissionProfile, error) {
	var rust rustPermissionProfileWire
	if err := json.Unmarshal(raw, &rust); err == nil && rust.Type != "" {
		profile, err := rust.toPermissionProfile()
		if err != nil {
			return nil, err
		}
		canonical, marshalErr := json.Marshal(rust)
		if marshalErr != nil {
			return nil, marshalErr
		}
		profile.runtimeJSON = string(canonical)
		return profile, nil
	}
	var profile PermissionProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func ParseRuntimePermissionProfileJSON(raw string) (*PermissionProfile, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%w: permission profile JSON is required", ErrInvalidSandboxRunRequest)
	}
	return parsePermissionProfileJSON(json.RawMessage(raw))
}

func RuntimePermissionProfileJSON(profile PermissionProfile) (string, error) {
	if strings.TrimSpace(profile.runtimeJSON) != "" {
		return profile.runtimeJSON, nil
	}
	return rustPermissionProfileJSON(profile)
}

// CanonicalPermissionProfile folds the legacy parallel JSON input into the
// profile once. A profile that already owns canonical runtime data always wins.
func CanonicalPermissionProfile(profile *PermissionProfile, legacyJSON string) (*PermissionProfile, error) {
	if profile == nil {
		return nil, fmt.Errorf("%w: permission profile is required", ErrInvalidSandboxRunRequest)
	}
	if strings.TrimSpace(profile.runtimeJSON) != "" || strings.TrimSpace(legacyJSON) == "" {
		clone := clonePermissionProfile(profile)
		return &clone, nil
	}
	parsed, err := ParseRuntimePermissionProfileJSON(legacyJSON)
	if err != nil {
		return nil, err
	}
	clone := clonePermissionProfile(parsed)
	return &clone, nil
}

type rustPermissionProfileWire struct {
	Type    string                    `json:"type"`
	FileSys *rustPermissionFilesystem `json:"file_system,omitempty"`
	Network string                    `json:"network,omitempty"`
}

type rustPermissionFilesystem struct {
	Type             string                          `json:"type"`
	Entries          []rustPermissionFilesystemEntry `json:"entries"`
	GlobScanMaxDepth *int                            `json:"glob_scan_max_depth,omitempty"`
}

type rustPermissionFilesystemEntry struct {
	Path   rustPermissionFilesystemPath `json:"path"`
	Access string                       `json:"access"`
}

type rustPermissionFilesystemPath struct {
	Type    string                          `json:"type"`
	Path    string                          `json:"path"`
	Pattern string                          `json:"pattern"`
	Value   rustPermissionFilesystemSpecial `json:"value"`
}

type rustPermissionFilesystemSpecial struct {
	Kind    string  `json:"kind"`
	Subpath *string `json:"subpath"`
}

func (r rustPermissionProfileWire) toPermissionProfile() (*PermissionProfile, error) {
	networkEnabled := r.Network == string(NetworkEnabled)
	switch r.Type {
	case "disabled":
		profile := FullAccessPermissionProfile()
		return &profile, nil
	case "external":
		profile := PermissionProfile{
			SandboxPolicy:  NewExternalSandboxPolicy(networkAccessFromBool(networkEnabled)),
			NetworkEnabled: networkEnabled,
		}
		return &profile, nil
	case "managed":
		policy := NewReadOnlyPolicy()
		deniedReadEntries := []FileSystemSandboxEntry{}
		if r.FileSys != nil && r.FileSys.Type == "unrestricted" {
			policy = NewDangerFullAccessPolicy()
		} else if r.FileSys != nil {
			slashTmpWritable := false
			ensureWorkspacePolicy := func() {
				if policy.Kind == SandboxWorkspaceWrite {
					return
				}
				policy = NewWorkspaceWritePolicy()
				policy.ExcludeSlashTmp = true
				policy.ExcludeTmpdirEnvVar = true
			}
			for _, entry := range r.FileSys.Entries {
				if !supportsSymbolicSlashTmp() && entry.Path.isSymbolicSlashTmp() {
					if strings.EqualFold(entry.Access, string(FileSystemAccessWrite)) {
						slashTmpWritable = true
					}
					continue
				}
				if strings.EqualFold(entry.Access, string(FileSystemAccessDeny)) {
					deniedReadEntries = append(deniedReadEntries, entry.toFileSystemSandboxEntry())
					continue
				}
				if strings.EqualFold(entry.Access, string(FileSystemAccessWrite)) {
					ensureWorkspacePolicy()
					if entry.Path.Type == "special" {
						switch strings.ToLower(entry.Path.Value.Kind) {
						case "project_roots", "current_working_directory":
							if entry.Path.Value.Subpath != nil && *entry.Path.Value.Subpath != "" {
								policy.WritableRoots = append(policy.WritableRoots, *entry.Path.Value.Subpath)
							}
						case "tmpdir":
							policy.ExcludeTmpdirEnvVar = false
						case "slash_tmp":
							policy.ExcludeSlashTmp = false
						default:
							if path := entry.Path.resolvedSandboxPolicyPath(); path != "" {
								policy.WritableRoots = append(policy.WritableRoots, path)
							}
						}
					} else if path := entry.Path.resolvedSandboxPolicyPath(); path != "" {
						policy.WritableRoots = append(policy.WritableRoots, path)
					}
				}
			}
			if policy.Kind == SandboxWorkspaceWrite && slashTmpWritable {
				policy.ExcludeSlashTmp = false
			}
			policy.fullDiskWriteAccess = r.FileSys.hasFullDiskWriteAccess()
			policy.fullDiskWriteAccessSet = true
		}
		policy.NetworkAccess = networkEnabled
		profile := PermissionProfile{SandboxPolicy: policy, NetworkEnabled: networkEnabled, DeniedReadEntries: deniedReadEntries}
		return &profile, nil
	default:
		return nil, fmt.Errorf("unsupported permission profile type %q", r.Type)
	}
}

func parseRustPermissionProfileWireJSON(raw []byte) (*rustPermissionProfileWire, error) {
	var profile rustPermissionProfileWire
	if err := json.Unmarshal(raw, &profile); err != nil {
		return nil, err
	}
	if profile.Type == "" {
		return nil, fmt.Errorf("%w: missing runtime permission profile type", ErrInvalidSandboxRunRequest)
	}
	return &profile, nil
}

func linuxPermissionProfileJSONForPlan(plan *CommandRunPlan) (string, error) {
	if plan == nil {
		return "", fmt.Errorf("%w: run plan is nil", ErrInvalidSandboxRunRequest)
	}
	if plan.PermissionProfile == nil {
		return "", fmt.Errorf("%w: permission profile is required", ErrInvalidSandboxRunRequest)
	}
	profileJSON, err := RuntimePermissionProfileJSON(*plan.PermissionProfile)
	if err != nil {
		return "", err
	}
	plan.PermissionProfileJSON = profileJSON
	return profileJSON, nil
}

func canonicalPermissionProfileFromJSON(raw []byte) (PermissionProfile, error) {
	profile, err := ParseRuntimePermissionProfileJSON(string(raw))
	if err != nil {
		return PermissionProfile{}, err
	}
	return clonePermissionProfile(profile), nil
}

// PermissionProfileWithAdditionalPermissions applies per-command grants to
// the canonical runtime profile without dropping read/deny entries.
func PermissionProfileWithAdditionalPermissions(profile *PermissionProfile, additional *AdditionalPermissionProfile) (*PermissionProfile, error) {
	if profile == nil {
		base := WorkspaceWritePermissionProfile()
		profile = &base
	}
	cloned := clonePermissionProfile(profile)
	if additional == nil || cloned.Disabled {
		return &cloned, nil
	}
	raw, err := RuntimePermissionProfileJSON(cloned)
	if err != nil {
		return nil, err
	}
	wire, err := parseRustPermissionProfileWireJSON([]byte(raw))
	if err != nil {
		return nil, err
	}
	if additional.Network != nil && *additional.Network {
		wire.Network = string(NetworkEnabled)
	}
	if len(additional.FileSystem) > 0 && wire.Type == "managed" {
		if wire.FileSys == nil {
			wire.FileSys = &rustPermissionFilesystem{Type: "restricted"}
		}
		if wire.FileSys.Type != "unrestricted" {
			if wire.FileSys.Type == "" {
				wire.FileSys.Type = "restricted"
			}
			for _, path := range additional.FileSystem {
				cleaned := cleanRunPath(path)
				if cleaned == "" || runtimeProfileHasPathAccess(wire.FileSys.Entries, cleaned, string(FileSystemAccessWrite)) {
					continue
				}
				wire.FileSys.Entries = append(wire.FileSys.Entries, rustPermissionFilesystemEntry{
					Path: rustPermissionFilesystemPath{Type: "path", Path: cleaned}, Access: string(FileSystemAccessWrite),
				})
			}
		}
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalPermissionProfileFromJSON(encoded)
	if err != nil {
		return nil, err
	}
	return &canonical, nil
}

func runtimeProfileHasPathAccess(entries []rustPermissionFilesystemEntry, path string, access string) bool {
	for _, entry := range entries {
		if entry.Access == access && entry.Path.Type == "path" && cleanRunPath(entry.Path.Path) == path {
			return true
		}
	}
	return false
}

func rustPermissionProfileJSON(profile PermissionProfile) (string, error) {
	data, err := json.Marshal(rustPermissionProfileWireFromPermissionProfile(&profile))
	if err != nil {
		return "", fmt.Errorf("failed to serialize permission profile: %w", err)
	}
	return string(data), nil
}

func mustRustPermissionProfileJSON(profile PermissionProfile) string {
	raw, err := rustPermissionProfileJSON(profile)
	if err != nil {
		panic(err)
	}
	return raw
}

func rustPermissionProfileWireFromPermissionProfile(profile *PermissionProfile) rustPermissionProfileWire {
	if profile == nil {
		readonly := ReadOnlyPermissionProfile()
		profile = &readonly
	}
	if profile.Disabled {
		return rustPermissionProfileWire{Type: "disabled"}
	}
	network := string(NetworkRestricted)
	if profile.AllowsNetwork() {
		network = string(NetworkEnabled)
	}
	policy := profile.LegacySandboxPolicy()
	if policy.HasFullDiskWriteAccess() && !profile.HasDenyReadEntries() {
		return rustPermissionProfileWire{
			Type:    "managed",
			FileSys: &rustPermissionFilesystem{Type: "unrestricted"},
			Network: network,
		}
	}
	entries := []rustPermissionFilesystemEntry{}
	addSpecial := func(access FileSystemAccessMode, kind string, subpath string) {
		var subpathPtr *string
		if subpath != "" {
			value := subpath
			subpathPtr = &value
		}
		entries = append(entries, rustPermissionFilesystemEntry{
			Path: rustPermissionFilesystemPath{
				Type:  "special",
				Value: rustPermissionFilesystemSpecial{Kind: kind, Subpath: subpathPtr},
			},
			Access: string(access),
		})
	}
	addPath := func(access FileSystemAccessMode, path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		entries = append(entries, rustPermissionFilesystemEntry{
			Path:   rustPermissionFilesystemPath{Type: "path", Path: path},
			Access: string(access),
		})
	}
	addDeniedReadEntries := func(denied []FileSystemSandboxEntry) {
		for _, entry := range denied {
			path := rustPermissionFilesystemPathFromFileSystemPath(entry.Path)
			if path.Type == "" {
				continue
			}
			entries = append(entries, rustPermissionFilesystemEntry{Path: path, Access: string(FileSystemAccessDeny)})
		}
	}
	switch policy.Kind {
	case SandboxWorkspaceWrite:
		addSpecial(FileSystemAccessRead, "root", "")
		addSpecial(FileSystemAccessWrite, "project_roots", "")
		if !policy.ExcludeSlashTmp {
			addSpecial(FileSystemAccessWrite, "slash_tmp", "")
		}
		if !policy.ExcludeTmpdirEnvVar {
			addSpecial(FileSystemAccessWrite, "tmpdir", "")
		}
		addSpecial(FileSystemAccessRead, "project_roots", ".git")
		addSpecial(FileSystemAccessRead, "project_roots", ".agents")
		addSpecial(FileSystemAccessRead, "project_roots", ".codex")
		for _, root := range policy.WritableRoots {
			addPath(FileSystemAccessWrite, root)
		}
	default:
		addSpecial(FileSystemAccessRead, "root", "")
	}
	addDeniedReadEntries(profile.DeniedReadEntries)
	return rustPermissionProfileWire{
		Type: "managed",
		FileSys: &rustPermissionFilesystem{
			Type:    "restricted",
			Entries: entries,
		},
		Network: network,
	}
}

func isRustPermissionProfileType(raw json.RawMessage, profileType string) bool {
	var wire struct {
		Type string `json:"type"`
	}
	return len(raw) > 0 && json.Unmarshal(raw, &wire) == nil && wire.Type == profileType
}

func (e rustPermissionFilesystemEntry) toFileSystemSandboxEntry() FileSystemSandboxEntry {
	return FileSystemSandboxEntry{
		Path:   e.Path.toFileSystemPath(),
		Access: FileSystemAccessMode(e.Access),
	}
}

func (p rustPermissionFilesystemPath) toFileSystemPath() FileSystemPath {
	switch p.Type {
	case "glob_pattern":
		return FileSystemPath{Type: "glob_pattern", Pattern: p.Pattern}
	case "special":
		return FileSystemPath{
			Type:  "special",
			Value: &FileSystemSpecialPath{Kind: p.Value.Kind, Subpath: p.Value.Subpath},
		}
	default:
		pathType := p.Type
		if pathType == "" {
			pathType = "path"
		}
		return FileSystemPath{Type: pathType, Path: p.Path}
	}
}

func rustPermissionFilesystemPathFromFileSystemPath(path FileSystemPath) rustPermissionFilesystemPath {
	switch path.Type {
	case "glob_pattern":
		return rustPermissionFilesystemPath{Type: "glob_pattern", Pattern: path.Pattern}
	case "special":
		if path.Value == nil {
			return rustPermissionFilesystemPath{}
		}
		return rustPermissionFilesystemPath{
			Type: "special",
			Value: rustPermissionFilesystemSpecial{
				Kind:    path.Value.Kind,
				Subpath: path.Value.Subpath,
			},
		}
	default:
		pathType := path.Type
		if pathType == "" {
			pathType = "path"
		}
		if strings.TrimSpace(path.Path) == "" {
			return rustPermissionFilesystemPath{}
		}
		return rustPermissionFilesystemPath{Type: pathType, Path: path.Path}
	}
}

func (p rustPermissionFilesystemPath) resolvedSandboxPolicyPath() string {
	switch p.Type {
	case "path":
		return p.Path
	case "special":
		switch p.Value.Kind {
		case "root":
			return string(filepath.Separator)
		case "project_roots", "current_working_directory":
			if p.Value.Subpath != nil && *p.Value.Subpath != "" {
				return *p.Value.Subpath
			}
			return "."
		case "slash_tmp":
			return slashTmpPath()
		case "tmpdir":
			return os.TempDir()
		}
	}
	return ""
}

func (p rustPermissionFilesystemPath) resolvedRuntimePath(cwd string) string {
	switch p.Type {
	case "path":
		return cleanRunPathWithCWD(p.Path, cwd)
	case "special":
		switch p.Value.Kind {
		case "root":
			return rootPathForCWD(cwd)
		case "project_roots", "current_working_directory":
			if p.Value.Subpath != nil && *p.Value.Subpath != "" {
				return cleanRunPathWithCWD(*p.Value.Subpath, cwd)
			}
			return cleanRunPath(cwd)
		case "slash_tmp":
			if slashTmp := slashTmpPath(); slashTmp != "" {
				return cleanRunPath(slashTmp)
			}
		case "tmpdir":
			return cleanRunPath(os.TempDir())
		}
	}
	return ""
}

func (p rustPermissionFilesystemPath) isSymbolicSlashTmp() bool {
	return p.Type == "special" && strings.EqualFold(p.Value.Kind, "slash_tmp")
}

func (f *rustPermissionFilesystem) hasFullDiskWriteAccess() bool {
	if f == nil {
		return false
	}
	if strings.EqualFold(f.Type, "unrestricted") {
		return true
	}
	rootWritable := false
	for _, entry := range f.Entries {
		if entry.Path.Type == "special" && strings.EqualFold(entry.Path.Value.Kind, "root") && strings.EqualFold(entry.Access, string(FileSystemAccessWrite)) {
			rootWritable = true
			break
		}
	}
	if !rootWritable {
		return false
	}
	for _, entry := range f.Entries {
		if strings.EqualFold(entry.Access, string(FileSystemAccessWrite)) {
			continue
		}
		if !supportsSymbolicSlashTmp() && entry.Path.isSymbolicSlashTmp() {
			continue
		}
		switch entry.Path.Type {
		case "glob_pattern":
			return false
		case "path":
			if !f.hasWriteOverrideForPath(entry.Path) {
				return false
			}
		case "special":
			switch strings.ToLower(entry.Path.Value.Kind) {
			case "root":
				if strings.EqualFold(entry.Access, string(FileSystemAccessDeny)) {
					return false
				}
			case "minimal":
			case "project_roots", "current_working_directory", "tmpdir", "slash_tmp":
				if !f.hasWriteOverrideForPath(entry.Path) {
					return false
				}
			default:
			}
		}
	}
	return true
}

func (f *rustPermissionFilesystem) hasWriteOverrideForPath(path rustPermissionFilesystemPath) bool {
	for _, candidate := range f.Entries {
		if strings.EqualFold(candidate.Access, string(FileSystemAccessWrite)) && permissionFilesystemPathsEqual(candidate.Path, path) {
			return true
		}
	}
	return false
}

func permissionFilesystemPathsEqual(left, right rustPermissionFilesystemPath) bool {
	if left.Type != right.Type {
		return false
	}
	switch left.Type {
	case "path":
		return cleanRunPath(left.Path) == cleanRunPath(right.Path)
	case "glob_pattern":
		return left.Pattern == right.Pattern
	case "special":
		if !strings.EqualFold(left.Value.Kind, right.Value.Kind) {
			return false
		}
		leftSubpath := ""
		if left.Value.Subpath != nil {
			leftSubpath = *left.Value.Subpath
		}
		rightSubpath := ""
		if right.Value.Subpath != nil {
			rightSubpath = *right.Value.Subpath
		}
		return leftSubpath == rightSubpath
	default:
		return false
	}
}

func runtimeAccessCanRead(access string) bool {
	return !strings.EqualFold(access, string(FileSystemAccessDeny)) && strings.TrimSpace(access) != ""
}

func runtimeAccessPrecedence(access string) int {
	switch {
	case strings.EqualFold(access, string(FileSystemAccessDeny)):
		return 3
	case strings.EqualFold(access, string(FileSystemAccessWrite)):
		return 2
	case strings.EqualFold(access, string(FileSystemAccessRead)):
		return 1
	default:
		return 0
	}
}

func pathComponentCount(path string) int {
	cleaned := cleanRunPath(path)
	if cleaned == "" {
		return 0
	}
	volume := filepath.VolumeName(cleaned)
	rest := strings.TrimPrefix(cleaned, volume)
	rest = strings.Trim(rest, string(filepath.Separator))
	if rest == "" {
		if volume != "" || filepath.IsAbs(cleaned) {
			return 1
		}
		return 0
	}
	return 1 + len(strings.Split(rest, string(filepath.Separator)))
}

func rootPathForCWD(cwd string) string {
	cleaned := cleanRunPath(cwd)
	if cleaned == "" {
		cleaned = cleanRunPath(string(filepath.Separator))
	}
	volume := filepath.VolumeName(cleaned)
	if volume != "" {
		return filepath.Clean(volume + string(filepath.Separator))
	}
	if filepath.IsAbs(cleaned) {
		return string(filepath.Separator)
	}
	return cleanRunPath(string(filepath.Separator))
}

func networkAccessFromBool(enabled bool) NetworkAccess {
	if enabled {
		return NetworkEnabled
	}
	return NetworkRestricted
}

func clonePermissionProfile(profile *PermissionProfile) PermissionProfile {
	if profile == nil {
		return ReadOnlyPermissionProfile()
	}
	clone := *profile
	if profile.SandboxPolicy != nil {
		policy := *profile.SandboxPolicy
		policy.WritableRoots = append([]string(nil), profile.SandboxPolicy.WritableRoots...)
		clone.SandboxPolicy = &policy
	}
	clone.DeniedReadEntries = append([]FileSystemSandboxEntry(nil), profile.DeniedReadEntries...)
	return clone
}

func cleanRunPathWithCWD(path string, cwd string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) && strings.TrimSpace(cwd) != "" {
		path = filepath.Join(cwd, path)
	}
	return cleanRunPath(path)
}

func cleanRunPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func CreateLinuxSandboxCommandArgsForPermissionProfile(
	command []string,
	commandCWD string,
	profile *PermissionProfile,
	sandboxPolicyCWD string,
	useLegacyLandlock bool,
	allowNetworkForProxy bool,
) ([]string, error) {
	if profile == nil {
		return nil, fmt.Errorf("%w: permission profile is required", ErrInvalidSandboxRunRequest)
	}
	profileJSON, err := rustPermissionProfileJSON(clonePermissionProfile(profile))
	if err != nil {
		return nil, err
	}
	return CreateLinuxSandboxCommandArgsForPermissionProfileJSON(
		command,
		commandCWD,
		profileJSON,
		sandboxPolicyCWD,
		useLegacyLandlock,
		allowNetworkForProxy,
	)
}

func CreateLinuxSandboxCommandArgsForPermissionProfileJSON(
	command []string,
	commandCWD string,
	profileJSON string,
	sandboxPolicyCWD string,
	useLegacyLandlock bool,
	allowNetworkForProxy bool,
) ([]string, error) {
	return CreateLinuxSandboxCommandArgsForPermissionProfileJSONWithSandboxExe("", command, commandCWD, profileJSON, sandboxPolicyCWD, useLegacyLandlock, allowNetworkForProxy)
}

func CreateLinuxSandboxCommandArgsForPermissionProfileJSONWithSandboxExe(
	sandboxExe string,
	command []string,
	commandCWD string,
	profileJSON string,
	sandboxPolicyCWD string,
	useLegacyLandlock bool,
	allowNetworkForProxy bool,
) ([]string, error) {
	args, err := linuxsandbox.CreateCommandArgsWithSandboxExe(sandboxExe, command, commandCWD, profileJSON, sandboxPolicyCWD, useLegacyLandlock, allowNetworkForProxy)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSandboxRunRequest, err)
	}
	return args, nil
}

func (p *CommandRunPlan) UnsupportedError() error {
	if p == nil || !p.RequiresPlatformSandbox {
		return nil
	}
	reason := strings.TrimSpace(p.UnsupportedReason)
	if reason == "" {
		reason = "requested sandbox options"
	}
	return fmt.Errorf("%w: %s requires platform sandbox support that is not available in this Go runtime yet", ErrPlatformSandboxUnsupported, reason)
}

func ResolvePermissionProfile(profileID string) (*PermissionProfile, string, error) {
	normalized := strings.TrimSpace(profileID)
	switch normalized {
	case BuiltInPermissionProfileDangerFullAccess, ":danger-full-access", "full-access":
		profile := FullAccessPermissionProfile()
		return &profile, normalized, nil
	case BuiltInPermissionProfileReadOnly, ":read-only":
		profile := ReadOnlyPermissionProfile()
		return &profile, normalized, nil
	case BuiltInPermissionProfileWorkspace, ":workspace", "workspace-write", "auto":
		profile := WorkspaceWritePermissionProfile()
		return &profile, normalized, nil
	default:
		return nil, "", fmt.Errorf("invalid permission profile: permission profile %q not found", profileID)
	}
}

func cloneSandboxStrings(values []string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, len(values))
	copy(out, values)
	return out
}
