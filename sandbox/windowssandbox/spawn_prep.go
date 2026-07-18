package windowssandbox

import (
	"fmt"
	"os"
	"sort"
	"strings"

	coresandbox "codex_go/sandbox"
)

type SpawnPrepOptions struct {
	InheritPath         bool
	AddGitSafeDirectory bool
}

type SpawnContext struct {
	Permissions           *ResolvedWindowsSandboxPermissions
	CurrentDir            string
	Env                   map[string]string
	LogsBaseDir           string
	UsesWriteCapabilities bool
}

type ElevatedSpawnContext struct {
	SandboxBase  string
	LogsBaseDir  string
	SandboxCreds *SandboxCredentials
	CapSIDs      []string
}

type LegacySessionSecurity struct {
	Token         uintptr
	ReadonlySID   string
	WriteRootSIDs []RootCapabilitySID
}

type RootCapabilitySID struct {
	Root string
	SID  string
}

type LegacyAclSIDs struct {
	ReadonlySID   string
	WriteRootSIDs []RootCapabilitySID
}

func PrepareSpawnContextCommon(req *CaptureRequest, options SpawnPrepOptions) (*SpawnContext, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	profile, err := ResolveCapturePermissionProfile(req)
	if err != nil {
		return nil, err
	}
	permissions, err := ResolvePermissions(profile, req.WorkspaceRoots)
	if err != nil {
		return nil, err
	}
	envMap := cloneEnv(req.Env)
	if envMap == nil {
		envMap = map[string]string{}
	}
	NormalizeNullDeviceEnv(envMap)
	EnsureNonInteractivePager(envMap)
	if options.InheritPath {
		InheritPathEnv(envMap)
	}
	if options.AddGitSafeDirectory {
		InjectGitSafeDirectory(envMap, req.CWD)
	}
	if err := EnsureCodexHomeExists(req.CodexHome); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(SandboxDir(req.CodexHome), 0o700); err != nil {
		return nil, err
	}
	if err := LogStart(req.Command, req.CodexHome); err != nil {
		return nil, err
	}
	return &SpawnContext{
		Permissions:           permissions,
		CurrentDir:            req.CWD,
		Env:                   envMap,
		LogsBaseDir:           SandboxDir(req.CodexHome),
		UsesWriteCapabilities: permissions.UsesWriteCapabilitiesForCWD(req.CWD, envMap),
	}, nil
}

func PrepareLegacySpawnContext(req *CaptureRequest, options SpawnPrepOptions) (*SpawnContext, error) {
	context, err := PrepareSpawnContextCommon(req, options)
	if err != nil {
		return nil, err
	}
	if context.Permissions.ShouldApplyNetworkBlock() {
		if err := ApplyNoNetworkToEnv(context.Env); err != nil {
			return nil, err
		}
	}
	return context, nil
}

func ResolveCapturePermissionProfile(req *CaptureRequest) (*coresandbox.PermissionProfile, error) {
	if req == nil {
		return nil, ErrInvalidRequest
	}
	if req.PermissionProfile != nil {
		return req.PermissionProfile, nil
	}
	profile, _, err := coresandbox.ResolvePermissionProfile(req.PermissionProfileID)
	return profile, err
}

func PrepareLegacySessionSecurity(usesWriteCapabilities bool, codexHome string, cwd string, capabilityRoots []string) (*LegacySessionSecurity, error) {
	caps, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		return nil, err
	}
	if usesWriteCapabilities {
		writeRootSIDs, err := RootCapabilitySIDs(codexHome, cwd, capabilityRoots)
		if err != nil {
			return nil, err
		}
		if len(writeRootSIDs) == 0 {
			return nil, fmt.Errorf("workspace-write sandbox has no writable root capability SIDs")
		}
		base, err := GetCurrentTokenForRestriction()
		if err != nil {
			return nil, err
		}
		defer CloseTokenHandle(base)
		token, err := CreateWorkspaceWriteTokenWithCapsFrom(base, rootCapabilitySIDStrings(writeRootSIDs))
		if err != nil {
			return nil, err
		}
		return &LegacySessionSecurity{Token: token, WriteRootSIDs: writeRootSIDs}, nil
	}
	base, err := GetCurrentTokenForRestriction()
	if err != nil {
		return nil, err
	}
	defer CloseTokenHandle(base)
	token, err := CreateReadonlyTokenWithCapsFrom(base, []string{caps.Readonly})
	if err != nil {
		return nil, err
	}
	return &LegacySessionSecurity{Token: token, ReadonlySID: caps.Readonly}, nil
}

func LegacySessionCapabilityRoots(permissions *ResolvedWindowsSandboxPermissions, currentDir string, envMap map[string]string, codexHome string) []string {
	allowPaths := ComputeAllowPathsForPermissions(permissions, currentDir, envMap).AllowSlice()
	if permissions != nil && permissions.UsesWriteCapabilitiesForCWD(currentDir, envMap) {
		return EffectiveWriteRootsForSetup(permissions, currentDir, envMap, codexHome, allowPaths, true)
	}
	return allowPaths
}

func RootCapabilitySIDs(codexHome string, cwd string, allowPaths []string) ([]RootCapabilitySID, error) {
	roots := canonicalDedupSorted(allowPaths)
	out := make([]RootCapabilitySID, 0, len(roots))
	for _, root := range roots {
		sid, err := WorkspaceWriteCapabilitySIDForRootWithCWD(codexHome, cwd, root)
		if err != nil {
			return nil, err
		}
		out = append(out, RootCapabilitySID{Root: root, SID: sid})
	}
	return out, nil
}

func MatchingRootCapability(path string, rootSIDs []RootCapabilitySID) *RootCapabilitySID {
	var best *RootCapabilitySID
	bestSpecificity := -1
	for i := range rootSIDs {
		rootSID := &rootSIDs[i]
		if !WorkspaceWriteRootContainsPath(rootSID.Root, path) {
			continue
		}
		specificity := WorkspaceWriteRootSpecificity(rootSID.Root)
		if specificity > bestSpecificity {
			best = rootSID
			bestSpecificity = specificity
		}
	}
	return best
}

func DenyRootCapabilitiesForPath(path string, rootSIDs []RootCapabilitySID) []RootCapabilitySID {
	var matching []RootCapabilitySID
	for _, rootSID := range rootSIDs {
		if WorkspaceWriteRootOverlapsPath(rootSID.Root, path) {
			matching = append(matching, rootSID)
		}
	}
	if len(matching) > 0 {
		return matching
	}
	return append([]RootCapabilitySID(nil), rootSIDs...)
}

func AllowNullDeviceForWorkspaceWrite(isWorkspaceWrite bool) {
	if !isWorkspaceWrite {
		return
	}
	// The concrete capability SIDs are granted in ApplyLegacySessionACLRules.
}

func ApplyLegacySessionACLRules(
	permissions *ResolvedWindowsSandboxPermissions,
	codexHome string,
	currentDir string,
	envMap map[string]string,
	additionalDenyReadPaths []string,
	additionalDenyWritePaths []string,
	aclSIDs LegacyAclSIDs,
) error {
	paths := ComputeAllowPathsForPermissions(permissions, currentDir, envMap)
	allow := paths.AllowSlice()
	denySet := paths.Deny
	for _, path := range additionalDenyWritePaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				if mkErr := os.MkdirAll(path, 0o700); mkErr != nil {
					return fmt.Errorf("create deny-write path %s: %w", path, mkErr)
				}
			} else {
				return err
			}
		}
		if canonical, ok := canonicalExistingPath(path); ok {
			denySet[canonical] = struct{}{}
		}
	}
	if aclSIDs.ReadonlySID != "" {
		for _, path := range allow {
			if err := EnsureAllowWriteACEs(ACLRequest{Path: path, SID: aclSIDs.ReadonlySID}); err != nil {
				return err
			}
		}
	} else {
		for _, path := range allow {
			rootSID := MatchingRootCapability(path, aclSIDs.WriteRootSIDs)
			if rootSID == nil {
				continue
			}
			if err := EnsureAllowWriteACEs(ACLRequest{Path: path, SID: rootSID.SID}); err != nil {
				return err
			}
		}
	}
	for _, path := range sortedStringSetKeys(denySet) {
		for _, rootSID := range DenyRootCapabilitiesForPath(path, aclSIDs.WriteRootSIDs) {
			if err := AddDenyWriteACE(ACLRequest{Path: path, SID: rootSID.SID}); err != nil {
				return err
			}
		}
	}
	if len(additionalDenyReadPaths) > 0 {
		if aclSIDs.ReadonlySID != "" {
			if _, err := SyncPersistentDenyReadACLs(codexHome, additionalDenyReadPaths, aclSIDs.ReadonlySID); err != nil {
				return err
			}
		} else {
			for _, rootSID := range aclSIDs.WriteRootSIDs {
				if _, err := SyncPersistentDenyReadACLs(codexHome, additionalDenyReadPaths, rootSID.SID); err != nil {
					return err
				}
			}
		}
	}
	for _, rootSID := range aclSIDs.WriteRootSIDs {
		if err := AllowNullDevice(rootSID.SID); err != nil {
			return err
		}
	}
	if aclSIDs.ReadonlySID != "" {
		if err := AllowNullDevice(aclSIDs.ReadonlySID); err != nil {
			return err
		}
	}
	if workspaceSID := MatchingRootCapability(currentDir, aclSIDs.WriteRootSIDs); workspaceSID != nil {
		if IsCommandCWDRoot(currentDir, workspaceSID.Root) {
			if _, err := ProtectWorkspaceCodexDir(currentDir, workspaceSID.SID); err != nil {
				return err
			}
			if _, err := ProtectWorkspaceAgentsDir(currentDir, workspaceSID.SID); err != nil {
				return err
			}
		}
	}
	return nil
}

func PrepareElevatedSpawnContextForPermissions(
	permissions *ResolvedWindowsSandboxPermissions,
	codexHome string,
	cwd string,
	envMap map[string]string,
	command []string,
	readRootsOverride []string,
	readRootsIncludePlatformDefaults bool,
	writeRootsOverride []string,
	writeRootsOverrideSet bool,
	denyReadPathsOverride []string,
	denyWritePathsOverride []string,
	proxyEnforced bool,
	proxySettingsMode ProxySettingsMode,
) (*ElevatedSpawnContext, error) {
	if permissions == nil {
		return nil, ErrInvalidRequest
	}
	NormalizeNullDeviceEnv(envMap)
	EnsureNonInteractivePager(envMap)
	InheritPathEnv(envMap)
	InjectGitSafeDirectory(envMap, cwd)

	sandboxBase := SandboxDir(codexHome)
	if err := EnsureCodexHomeExists(sandboxBase); err != nil {
		return nil, err
	}
	if err := LogStart(command, codexHome); err != nil {
		return nil, err
	}
	usesWriteCapabilities := permissions.UsesWriteCapabilitiesForCWD(cwd, envMap)
	allowDeny := ComputeAllowPathsForPermissions(permissions, cwd, envMap)
	writeRoots := allowDeny.AllowSlice()
	denyWritePaths := allowDeny.DenySlice()
	setupWriteRootsOverride := writeRootsOverride
	setupWriteRootsOverrideSet := writeRootsOverrideSet
	effectiveWriteRoots := []string{}
	if usesWriteCapabilities {
		if !setupWriteRootsOverrideSet {
			setupWriteRootsOverride = writeRoots
			setupWriteRootsOverrideSet = true
		}
		effectiveWriteRoots = EffectiveWriteRootsForSetup(permissions, cwd, envMap, codexHome, setupWriteRootsOverride, setupWriteRootsOverrideSet)
		setupWriteRootsOverride = effectiveWriteRoots
		setupWriteRootsOverrideSet = true
	}
	if len(denyWritePathsOverride) == 0 {
		denyWritePathsOverride = denyWritePaths
	}

	sandboxCreds, err := RequireLogonSandboxCredsForPermissions(
		permissions,
		cwd,
		envMap,
		codexHome,
		readRootsOverride,
		readRootsOverride != nil,
		readRootsIncludePlatformDefaults,
		setupWriteRootsOverride,
		setupWriteRootsOverrideSet,
		denyReadPathsOverride,
		denyWritePathsOverride,
		proxyEnforced,
		proxySettingsMode,
	)
	if err != nil {
		return nil, err
	}
	caps, err := LoadOrCreateCapabilitySIDs(codexHome)
	if err != nil {
		return nil, err
	}
	var sidForNull string
	var capSIDs []string
	if usesWriteCapabilities {
		rootSIDs, err := RootCapabilitySIDs(codexHome, cwd, effectiveWriteRoots)
		if err != nil {
			return nil, err
		}
		capSIDs = rootCapabilitySIDStrings(rootSIDs)
		if len(capSIDs) == 0 {
			return nil, fmt.Errorf("workspace-write sandbox has no writable root capability SIDs")
		}
		sidForNull = capSIDs[0]
	} else {
		sidForNull = caps.Readonly
		capSIDs = []string{caps.Readonly}
	}
	if err := AllowNullDevice(sidForNull); err != nil {
		return nil, err
	}
	return &ElevatedSpawnContext{
		SandboxBase:  sandboxBase,
		LogsBaseDir:  sandboxBase,
		SandboxCreds: sandboxCreds,
		CapSIDs:      capSIDs,
	}, nil
}

func canonicalDedupSorted(paths []string) []string {
	seen := map[string]string{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		canonical, err := CanonicalizePath(path)
		if err != nil {
			canonical = cleanWindowsSandboxAbs(path)
		}
		if canonical == "" {
			continue
		}
		seen[CanonicalPathKey(canonical)] = canonical
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, seen[key])
	}
	return out
}

func rootCapabilitySIDStrings(rootSIDs []RootCapabilitySID) []string {
	out := make([]string, 0, len(rootSIDs))
	for _, rootSID := range rootSIDs {
		if strings.TrimSpace(rootSID.SID) != "" {
			out = append(out, rootSID.SID)
		}
	}
	return out
}
