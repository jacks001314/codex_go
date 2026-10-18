package config

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"codex_go/sandbox"
	"codex_go/utils"

	json "github.com/goccy/go-json"
	"github.com/mitchellh/go-homedir"
)

const projectRootsPatternPrefix = "codex-project-roots://"

type SandboxPermissionProfileResolution struct {
	ID             string
	Profile        *sandbox.PermissionProfile
	ProfileJSON    string
	WorkspaceRoots []string
	// GlobScanMaxDepth is the configured deny-glob expansion limit for managed
	// restricted profiles; nil means unbounded or not applicable.
	GlobScanMaxDepth *int
}

// PermissionProfileSelection is the uncompiled result of
// ResolvePermissionProfileSelection (Rust ResolvedPermissionProfileSelection,
// #39752): the effective profile id and the merged configured + managed
// profile catalog without platform-specific path compilation.
type PermissionProfileSelection struct {
	ProfileID string
	Profiles  map[string]any
}

// ResolvePermissionProfileSelection mirrors Rust resolve_permission_profile_selection
// (#39752): the effective profile is selected using configured defaults, managed
// requirements defaults, and allowlists, and the merged catalog is returned
// without compiling executor paths.
func (c *Config) ResolvePermissionProfileSelection() (PermissionProfileSelection, error) {
	if c == nil {
		c = &Config{Values: map[string]any{}}
	}
	configuredProfiles := permissionProfilesFromConfig(c.Values["permissions"])
	configuredDefault := strings.TrimSpace(stringFromConfigValue(c.Values["default_permissions"]))
	var requirements *ConfigRequirements
	if c.Requirements != nil {
		requirements = c.Requirements
	}
	merged, err := mergePermissionProfiles(configuredProfiles, requirements)
	if err != nil {
		return PermissionProfileSelection{}, err
	}
	// Rust resolve_default_permissions: the managed default is the fallback, not
	// the initial selection, and a disallowed configured default falls back with
	// a startup warning.
	profileID, _, err := c.resolveRequirementDefaultPermissionProfile(configuredDefault)
	if err != nil {
		return PermissionProfileSelection{}, err
	}
	if err := validateAllowedPermissionProfiles(requirements, merged); err != nil {
		return PermissionProfileSelection{}, err
	}
	return PermissionProfileSelection{ProfileID: profileID, Profiles: merged}, nil
}

// validateAllowedPermissionProfiles mirrors Rust's
// validate_required_permission_profile_catalog allow-list check: every entry in
// allowed_permission_profiles, enabled or not, must name a built-in or a
// defined profile.
func validateAllowedPermissionProfiles(requirements *ConfigRequirements, profiles map[string]any) error {
	if requirements == nil || requirements.AllowedPermissionProfiles == nil {
		return nil
	}
	for allowed := range requirements.AllowedPermissionProfiles {
		allowed = strings.TrimSpace(allowed)
		if allowed == "" {
			continue
		}
		if !permissionProfileKnown(profiles, allowed) {
			return fmt.Errorf("requirements.toml allowed_permission_profiles refers to undefined profile `%s`", allowed)
		}
	}
	return nil
}

// mergePermissionProfiles mirrors Rust's merge_managed_permission_profiles: the
// managed catalog is merged into the configured one, and a managed profile that
// shadows a distinct config-defined name is rejected.
//
// Go's effective-config loader also copies the managed catalog into the
// `[permissions]` values so the app-server can list the profiles, so an
// identical entry in both layers is expected rather than a conflict.
func mergePermissionProfiles(configured map[string]any, requirements *ConfigRequirements) (map[string]any, error) {
	merged := cloneMap(configured)
	if merged == nil {
		merged = map[string]any{}
	}
	if requirements == nil {
		return merged, nil
	}
	for name, profile := range requirements.Permissions {
		if existing, exists := merged[name]; exists {
			if reflect.DeepEqual(existing, profile) {
				continue
			}
			return nil, fmt.Errorf("requirements.toml permissions profile `%s` conflicts with a config-defined profile of the same name", name)
		}
		merged[name] = profile
	}
	return merged, nil
}

// requirementDefaultPermissionProfile mirrors Rust's implicit_default_permissions:
// the workspace profile is the implicit managed default only when the allow-list
// permits both the workspace and read-only built-ins.
func requirementDefaultPermissionProfile(allowed map[string]bool) (string, bool) {
	if permissionProfileAllowedByRequirements(allowed, sandbox.BuiltInPermissionProfileWorkspace) &&
		permissionProfileAllowedByRequirements(allowed, sandbox.BuiltInPermissionProfileReadOnly) {
		return ":workspace", true
	}
	return "", false
}

// permissionProfileAllowedByRequirements mirrors Rust's is_permission_allowed:
// the allow-list entry must be exactly true. Go additionally accepts the legacy
// non-colon spelling of the built-in profiles.
func permissionProfileAllowedByRequirements(allowed map[string]bool, profileID string) bool {
	if allowed == nil {
		return true
	}
	if allowed[profileID] {
		return true
	}
	aliases := map[string]string{
		sandbox.BuiltInPermissionProfileWorkspace:        ":workspace",
		sandbox.BuiltInPermissionProfileReadOnly:         ":read-only",
		sandbox.BuiltInPermissionProfileDangerFullAccess: ":danger-full-access",
		":workspace":          sandbox.BuiltInPermissionProfileWorkspace,
		":read-only":          sandbox.BuiltInPermissionProfileReadOnly,
		":danger-full-access": sandbox.BuiltInPermissionProfileDangerFullAccess,
	}
	if alias, ok := aliases[profileID]; ok {
		return allowed[alias]
	}
	return false
}

// PermissionProfileAllowedByRequirements reports whether the managed
// allowed_permission_profiles allow-list admits profileID, resolving the
// built-in `:workspace` / `:read-only` / `:danger-full-access` aliases. A nil
// allow-list admits every profile. It is the exported form of
// permissionProfileAllowedByRequirements (Rust `is_permission_allowed`), used by
// the #49160 projectless defaults gate.
func PermissionProfileAllowedByRequirements(allowed map[string]bool, profileID string) bool {
	return permissionProfileAllowedByRequirements(allowed, profileID)
}

// PermissionProfileSatisfiesRequirements reports whether a saved, unnamed
// runtime profile may be restored under the effective managed requirements
// (Rust #53419 `permission_profile_satisfies_requirements`). A managed
// allow-list makes the configured default authoritative, so a saved profile is
// not eligible. When managed sandbox modes are present, the saved profile's
// legacy mode must be one of them. Managed deny-read entries are applied
// separately while restoring the profile.
func (c *Config) PermissionProfileSatisfiesRequirements(profile *sandbox.PermissionProfile) bool {
	if profile == nil {
		return false
	}
	if profile.SandboxPolicy != nil && profile.SandboxPolicy.Kind == sandbox.SandboxModeExternalSandbox {
		return false
	}
	if c == nil || c.Requirements == nil {
		return true
	}
	requirements := c.Requirements
	if requirements.AllowedPermissionProfiles != nil {
		return false
	}
	if len(requirements.AllowedSandboxModes) == 0 {
		return true
	}
	mode := sandbox.SandboxMode(profile.LegacySandboxPolicy().Kind)
	for _, allowed := range requirements.AllowedSandboxModes {
		if allowed == mode {
			return true
		}
	}
	return false
}

// resolveRequirementDefaultPermissionProfile mirrors Rust's
// resolve_default_permissions: when a managed allow-list is present, the
// selected profile falls back to the required default - the requirement's
// `default_permissions` or the implicit workspace profile - and a disallowed
// selection produces a startup warning instead of an error. The returned
// warning is empty when no fallback happened.
func (c *Config) resolveRequirementDefaultPermissionProfile(selected string) (string, string, error) {
	if c == nil || c.Requirements == nil {
		return strings.TrimSpace(selected), "", nil
	}
	requirements := c.Requirements
	if requirements.AllowedPermissionProfiles == nil {
		// Rust rejects this configuration at requirements load
		// (default_permissions requires allowed_permission_profiles); direct
		// in-memory requirements keep the managed default as the selection.
		if strings.TrimSpace(selected) == "" && requirements.DefaultPermissions != nil {
			return strings.TrimSpace(*requirements.DefaultPermissions), "", nil
		}
		return strings.TrimSpace(selected), "", nil
	}
	allowed := requirements.AllowedPermissionProfiles
	fallback := ""
	if requirements.DefaultPermissions != nil {
		fallback = strings.TrimSpace(*requirements.DefaultPermissions)
	}
	if fallback == "" {
		if implicit, ok := requirementDefaultPermissionProfile(allowed); ok {
			fallback = implicit
		}
	}
	if fallback == "" {
		return "", "", fmt.Errorf("requirements.toml default_permissions must be set unless allowed_permission_profiles allows both `:workspace` and `:read-only`")
	}
	if !permissionProfileAllowedByRequirements(allowed, fallback) {
		return "", "", fmt.Errorf("requirements.toml default_permissions `%s` must be allowed by allowed_permission_profiles", fallback)
	}
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return fallback, "", nil
	}
	if permissionProfileAllowedByRequirements(allowed, selected) {
		return selected, "", nil
	}
	warning := fmt.Sprintf(
		"Configured value for `permission_profile` is disallowed by requirements; falling back from `%s` to required value `%s`.",
		selected, fallback)
	return fallback, warning, nil
}

// PermissionProfileRequirementWarning reports the startup warning for a
// configured default that managed requirements disallow, if any.
func (c *Config) PermissionProfileRequirementWarning() string {
	if c == nil {
		return ""
	}
	configured := stringFromConfigValue(c.Values["default_permissions"])
	return c.PermissionProfileRequirementWarningFor(configured)
}

// PermissionProfileRequirementWarningFor reports the requirement fallback
// warning for an explicit profile selection. The startup config path accepts the
// fallback; an explicit request override must be rejected instead (Rust's
// app-server checks the startup warning for the override-driven load).
func (c *Config) PermissionProfileRequirementWarningFor(selected string) string {
	if c == nil {
		return ""
	}
	_, warning, err := c.resolveRequirementDefaultPermissionProfile(selected)
	if err != nil {
		return ""
	}
	return warning
}

func permissionProfileKnown(profiles map[string]any, profileID string) bool {
	if strings.HasPrefix(profileID, ":") {
		return isRuntimeBuiltinPermissionProfile(profileID)
	}
	// Go accepts the legacy non-colon spelling of the built-in profiles.
	if isBuiltinPermissionProfileName(profileID) {
		return true
	}
	_, ok := profiles[profileID]
	return ok
}

func isBuiltinPermissionProfileName(profileID string) bool {
	switch strings.TrimPrefix(profileID, ":") {
	case sandbox.BuiltInPermissionProfileReadOnly,
		sandbox.BuiltInPermissionProfileWorkspace,
		sandbox.BuiltInPermissionProfileDangerFullAccess:
		return true
	default:
		return false
	}
}

type runtimePermissionProfileWire struct {
	Type       string                 `json:"type"`
	FileSystem *runtimeFilesystemWire `json:"file_system,omitempty"`
	Network    sandbox.NetworkAccess  `json:"network,omitempty"`
}

type runtimeFilesystemWire struct {
	Type             string                           `json:"type"`
	Entries          []sandbox.FileSystemSandboxEntry `json:"entries"`
	GlobScanMaxDepth *int                             `json:"glob_scan_max_depth,omitempty"`
}

type permissionProfileBuilder struct {
	entries                []sandbox.FileSystemSandboxEntry
	entryIndex             map[string]int
	pathContext            *ConfigPathContext
	globScanMaxDepth       *int
	network                sandbox.NetworkAccess
	disabled               bool
	unrestrictedFilesystem bool
	workspaceRoots         []string
	workspaceRootEnabled   map[string]bool
}

func (c *Config) ResolveSandboxPermissionProfile(profileID string, cwd string) (*SandboxPermissionProfileResolution, error) {
	return c.ResolveSandboxPermissionProfileWithWorkspaceRoots(profileID, cwd, nil)
}

// EffectivePermissionProfileForCWD resolves the permission profile the startup
// sandbox warning inspects: the default profile of the layers visible from cwd
// (Rust `config.permissions.permission_profile()`). A config that selects no
// permission profile resolves to the built-in read-only default, matching the
// runtime's own default, and an unresolvable config reports nil so a caller
// skips the warning instead of failing.
func (s *ConfigService) EffectivePermissionProfileForCWD(cwd string) *sandbox.PermissionProfile {
	if s == nil {
		return nil
	}
	layers, err := s.readLayersForCWD(cwd, s.currentProfile())
	if err != nil {
		return nil
	}
	values, _ := mergeConfigLayers(layers)
	cfg := &Config{Values: values, Requirements: s.LocalRequirements()}
	if !permissionProfileSelectionConfigured(values) {
		// Rust's PermissionProfile::default() is managed read-only, and the Go
		// runtime already derives that default (loadSandboxRunConfigForRunContext),
		// so the startup warning must inspect the same profile.
		if cfg.Values == nil {
			cfg.Values = map[string]any{}
		}
		cfg.Values["sandbox_mode"] = string(sandbox.SandboxReadOnly)
	}
	resolution, err := cfg.ResolveSandboxPermissionProfile("", cwd)
	if err != nil || resolution == nil {
		return nil
	}
	return resolution.Profile
}

// permissionProfileSelectionConfigured reports whether the merged config picks a
// permission profile, either through the canonical `default_permissions` key or
// the legacy `sandbox_mode`.
func permissionProfileSelectionConfigured(values map[string]any) bool {
	if values == nil {
		return false
	}
	if strings.TrimSpace(stringFromConfigValue(values["default_permissions"])) != "" {
		return true
	}
	return strings.TrimSpace(stringFromConfigValue(values["sandbox_mode"])) != ""
}

// ResolveSandboxPermissionProfileWithWorkspaceRoots mirrors Rust's
// `PermissionProfile::materialize_project_roots_with_path_uris` (#46568): the
// profile's project roots are anchored at the supplied workspace roots (a
// selected environment's workspace) instead of the thread cwd. An empty list
// keeps the cwd behavior.
func (c *Config) ResolveSandboxPermissionProfileWithWorkspaceRoots(profileID string, cwd string, workspaceRoots []string) (*SandboxPermissionProfileResolution, error) {
	if c == nil {
		c = &Config{Values: map[string]any{}}
	}
	pathContext, err := NewNativeConfigPathContext(cwd)
	if err != nil {
		return nil, err
	}
	profiles, err := mergePermissionProfiles(permissionProfilesFromConfig(c.Values["permissions"]), c.Requirements)
	if err != nil {
		return nil, err
	}
	if err := validateConfigPermissionProfileNames(profiles); err != nil {
		return nil, err
	}
	if c.Requirements != nil {
		if err := validatePermissionProfileRequirements(c.Requirements); err != nil {
			return nil, err
		}
		if err := validateAllowedPermissionProfiles(c.Requirements, profiles); err != nil {
			return nil, err
		}
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = stringFromConfigValue(c.Values["default_permissions"])
	}
	// A disallowed selection falls back to the required default rather than
	// failing (Rust apply_requirement_constrained_value).
	resolvedProfileID, _, err := c.resolveRequirementDefaultPermissionProfile(profileID)
	if err != nil {
		return nil, err
	}
	profileID = resolvedProfileID
	if profileID == "" {
		if len(profiles) > 0 {
			return nil, fmt.Errorf("config defines `[permissions]` profiles but does not set `default_permissions`")
		}
		return c.resolveLegacySandboxPermissionProfileWithWorkspaceRoots(cwd, workspaceRoots)
	}
	if !strings.HasPrefix(profileID, ":") {
		if _, ok := profiles[profileID]; ok {
			builder, err := compileConfigPermissionProfile(profiles, profileID, map[string]bool{}, pathContext)
			if err != nil {
				return nil, err
			}
			return runtimeResolutionFromBuilderWithWorkspaceRoots(profileID, builder, cwd, workspaceRoots)
		}
	}
	if strings.HasPrefix(profileID, ":") {
		if !isRuntimeBuiltinPermissionProfile(profileID) {
			return nil, fmt.Errorf("default_permissions refers to unknown built-in profile `%s`", profileID)
		}
	}
	if profile, _, err := sandbox.ResolvePermissionProfile(profileID); err == nil {
		raw, err := sandbox.RuntimePermissionProfileJSON(*profile)
		if err != nil {
			return nil, err
		}
		return &SandboxPermissionProfileResolution{ID: profileID, Profile: profile, ProfileJSON: raw}, nil
	}
	if len(profiles) == 0 {
		return nil, fmt.Errorf("default_permissions requires a `[permissions]` table")
	}
	return nil, fmt.Errorf("permission profile %q not found", profileID)
}

func (c *Config) PermissionProfileSummaries() ([]sandbox.PermissionProfileSummary, error) {
	if c == nil {
		return PermissionProfileSummariesFromValues(nil)
	}
	return PermissionProfileSummariesFromValues(c.Values)
}

func PermissionProfileSummariesFromValues(values map[string]any) ([]sandbox.PermissionProfileSummary, error) {
	return PermissionProfileSummariesFromValuesForCWD(values, "")
}

// PermissionProfileSummariesFromValuesForCWD builds the profile catalog using
// the requested execution cwd. A custom profile whose configured roots or
// filesystem paths cannot be compiled safely is reported as unavailable rather
// than being offered to clients.
func PermissionProfileSummariesFromValuesForCWD(values map[string]any, cwd string) ([]sandbox.PermissionProfileSummary, error) {
	if values == nil {
		values = map[string]any{}
	}
	profiles := permissionProfilesFromConfig(values["permissions"])
	if err := validateConfigPermissionProfileNames(profiles); err != nil {
		return nil, err
	}
	pathContext, err := NewNativeConfigPathContext(cwd)
	if err != nil {
		return nil, err
	}
	summaries := sandbox.BuiltinPermissionProfileSummaries()
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		description := ""
		if profile, ok := profiles[name].(map[string]any); ok {
			description = stringFromConfigValue(profile["description"])
		}
		builder, compileErr := compileConfigPermissionProfile(profiles, name, map[string]bool{}, pathContext)
		if compileErr == nil {
			_, compileErr = runtimeResolutionFromBuilderWithWorkspaceRoots(name, builder, cwd, nil)
		}
		summaries = append(summaries, sandbox.PermissionProfileSummary{
			ID:          name,
			Description: description,
			Allowed:     compileErr == nil,
		})
	}
	return summaries, nil
}

func (c *Config) resolveLegacySandboxPermissionProfile(cwd string) (*SandboxPermissionProfileResolution, error) {
	return c.resolveLegacySandboxPermissionProfileWithWorkspaceRoots(cwd, nil)
}

// resolveLegacySandboxPermissionProfileWithWorkspaceRoots keeps the legacy
// sandbox_mode resolution; the captured workspace roots only affect the
// workspace-write profile set the same way the profile's anchored root does.
func (c *Config) resolveLegacySandboxPermissionProfileWithWorkspaceRoots(cwd string, workspaceRoots []string) (*SandboxPermissionProfileResolution, error) {
	modeText := stringFromConfigValue(c.Values["sandbox_mode"])
	if modeText == "" {
		return nil, nil
	}
	mode, err := sandbox.ParseSandboxMode(modeText)
	if err != nil {
		return nil, err
	}
	var profile sandbox.PermissionProfile
	switch mode {
	case sandbox.SandboxReadOnly:
		profile = sandbox.ReadOnlyPermissionProfile()
	case sandbox.SandboxDangerFullAccess:
		profile = sandbox.FullAccessPermissionProfile()
	case sandbox.SandboxWorkspaceWrite:
		profile = sandbox.WorkspaceWritePermissionProfile()
		applyLegacyWorkspaceWriteConfig(&profile, c.Values["sandbox_workspace_write"], turnWorkspaceRootOrCWD(workspaceRoots, cwd))
	default:
		return nil, fmt.Errorf("unknown sandbox mode %q", modeText)
	}
	raw, err := sandbox.RuntimePermissionProfileJSON(profile)
	if err != nil {
		return nil, err
	}
	return &SandboxPermissionProfileResolution{ID: string(mode), Profile: &profile, ProfileJSON: raw}, nil
}

func applyLegacyWorkspaceWriteConfig(profile *sandbox.PermissionProfile, raw any, cwd string) {
	policy := profile.LegacySandboxPolicy()
	values, ok := raw.(map[string]any)
	if !ok {
		profile.SandboxPolicy = policy
		return
	}
	if roots := stringListFromConfigValue(values["writable_roots"]); len(roots) > 0 {
		for _, root := range roots {
			if resolved := resolveConfigPath(root, cwd); resolved != "" {
				policy.WritableRoots = append(policy.WritableRoots, resolved)
			}
		}
	}
	if networkAccess, ok := values["network_access"].(bool); ok {
		policy.NetworkAccess = networkAccess
		profile.NetworkEnabled = networkAccess
	}
	if excludeTmp, ok := values["exclude_tmpdir_env_var"].(bool); ok {
		policy.ExcludeTmpdirEnvVar = excludeTmp
	}
	if excludeSlashTmp, ok := values["exclude_slash_tmp"].(bool); ok {
		policy.ExcludeSlashTmp = excludeSlashTmp
	}
	profile.SandboxPolicy = policy
}

func permissionProfilesFromConfig(raw any) map[string]any {
	profiles, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	return profiles
}

func validateConfigPermissionProfileNames(profiles map[string]any) error {
	for name := range profiles {
		if strings.HasPrefix(name, ":") {
			return fmt.Errorf("permissions profile `%s` uses a reserved built-in profile prefix", name)
		}
	}
	return nil
}

func compileConfigPermissionProfile(
	profiles map[string]any,
	profileID string,
	stack map[string]bool,
	pathContext *ConfigPathContext,
) (*permissionProfileBuilder, error) {
	if strings.HasPrefix(profileID, ":") {
		if isRuntimeBuiltinPermissionProfile(profileID) {
			builder, err := builtinPermissionProfileBuilder(profileID)
			if err != nil {
				return nil, err
			}
			builder.pathContext = pathContext
			return builder, nil
		}
		return nil, fmt.Errorf("default_permissions refers to unknown built-in profile `%s`", profileID)
	}
	if stack[profileID] {
		return nil, fmt.Errorf("permissions profile %q extends itself", profileID)
	}
	raw, ok := profiles[profileID]
	if !ok {
		return nil, fmt.Errorf("permission profile %q not found", profileID)
	}
	profile, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("permissions profile %q is not a table", profileID)
	}
	stack[profileID] = true
	builder := newPermissionProfileBuilder(pathContext)
	if parent := stringFromConfigValue(profile["extends"]); parent != "" {
		parentBuilder, err := compileConfigPermissionProfile(profiles, parent, stack, pathContext)
		if err != nil {
			return nil, err
		}
		builder = parentBuilder.clone()
	}
	delete(stack, profileID)
	if roots, ok := profile["workspace_roots"].(map[string]any); ok {
		builder.applyWorkspaceRoots(roots)
	}
	if filesystem, ok := profile["filesystem"].(map[string]any); ok {
		if err := builder.applyFilesystem(filesystem); err != nil {
			return nil, fmt.Errorf("permissions profile %q: %w", profileID, err)
		}
	}
	if network, ok := profile["network"].(map[string]any); ok {
		if enabled, ok := network["enabled"].(bool); ok {
			if enabled {
				builder.network = sandbox.NetworkEnabled
			} else {
				builder.network = sandbox.NetworkRestricted
			}
		}
	}
	return builder, nil
}

func newPermissionProfileBuilder(pathContext *ConfigPathContext) *permissionProfileBuilder {
	return &permissionProfileBuilder{
		entryIndex:           map[string]int{},
		pathContext:          pathContext,
		network:              sandbox.NetworkRestricted,
		workspaceRootEnabled: map[string]bool{},
	}
}

func builtinPermissionProfileBuilder(profileID string) (*permissionProfileBuilder, error) {
	builder := newPermissionProfileBuilder(nil)
	switch profileID {
	case ":danger-full-access", sandbox.BuiltInPermissionProfileDangerFullAccess:
		builder.disabled = true
		builder.unrestrictedFilesystem = true
		builder.network = sandbox.NetworkEnabled
	case ":read-only", sandbox.BuiltInPermissionProfileReadOnly:
		builder.addEntry(runtimeSpecialEntry("root", "", sandbox.FileSystemAccessRead))
	case ":workspace", sandbox.BuiltInPermissionProfileWorkspace:
		builder.addEntry(runtimeSpecialEntry("root", "", sandbox.FileSystemAccessRead))
		builder.addEntry(runtimeSpecialEntry("project_roots", "", sandbox.FileSystemAccessWrite))
		builder.addEntry(runtimeSpecialEntry("slash_tmp", "", sandbox.FileSystemAccessWrite))
		builder.addEntry(runtimeSpecialEntry("tmpdir", "", sandbox.FileSystemAccessWrite))
		builder.addEntry(runtimeSpecialEntry("project_roots", ".git", sandbox.FileSystemAccessRead))
		builder.addEntry(runtimeSpecialEntry("project_roots", ".agents", sandbox.FileSystemAccessRead))
		builder.addEntry(runtimeSpecialEntry("project_roots", ".gcode", sandbox.FileSystemAccessRead))
		// AWS profiles can select credential helpers that the application
		// executes, so the metadata path stays protected by default even though
		// the project root itself is writable (Rust #48176).
		builder.addEntry(runtimeSpecialEntry("project_roots", ".aws", sandbox.FileSystemAccessRead))
	default:
		return nil, fmt.Errorf("default_permissions refers to unknown built-in profile `%s`", profileID)
	}
	return builder, nil
}

func (b *permissionProfileBuilder) clone() *permissionProfileBuilder {
	clone := newPermissionProfileBuilder(b.pathContext)
	clone.entries = append([]sandbox.FileSystemSandboxEntry(nil), b.entries...)
	clone.entryIndex = make(map[string]int, len(b.entryIndex))
	for key, value := range b.entryIndex {
		clone.entryIndex[key] = value
	}
	if b.globScanMaxDepth != nil {
		value := *b.globScanMaxDepth
		clone.globScanMaxDepth = &value
	}
	clone.network = b.network
	clone.disabled = b.disabled
	clone.unrestrictedFilesystem = b.unrestrictedFilesystem
	clone.workspaceRoots = append([]string(nil), b.workspaceRoots...)
	clone.workspaceRootEnabled = make(map[string]bool, len(b.workspaceRootEnabled))
	for key, value := range b.workspaceRootEnabled {
		clone.workspaceRootEnabled[key] = value
	}
	return clone
}

func (b *permissionProfileBuilder) applyWorkspaceRoots(values map[string]any) {
	paths := make([]string, 0, len(values))
	for rawPath := range values {
		paths = append(paths, rawPath)
	}
	sort.Strings(paths)
	for _, rawPath := range paths {
		rawEnabled := values[rawPath]
		enabled, ok := rawEnabled.(bool)
		if !ok {
			continue
		}
		if _, seen := b.workspaceRootEnabled[rawPath]; !seen {
			b.workspaceRoots = append(b.workspaceRoots, rawPath)
		}
		b.workspaceRootEnabled[rawPath] = enabled
	}
}

func (b *permissionProfileBuilder) applyFilesystem(values map[string]any) error {
	if depth := positiveIntFromConfigValue(values["glob_scan_max_depth"]); depth != nil {
		b.globScanMaxDepth = depth
	}
	pathKeys := make([]string, 0, len(values))
	for pathKey := range values {
		pathKeys = append(pathKeys, pathKey)
	}
	sort.Strings(pathKeys)
	for _, pathKey := range pathKeys {
		rawPermission := values[pathKey]
		if pathKey == "glob_scan_max_depth" {
			continue
		}
		switch permission := rawPermission.(type) {
		case string:
			access, err := parseFilesystemAccess(permission)
			if err != nil {
				return err
			}
			entries, err := compileFilesystemAccess(pathKey, access, b.pathContext)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				b.addEntry(entry)
			}
		case map[string]any:
			subpaths := make([]string, 0, len(permission))
			for subpath := range permission {
				subpaths = append(subpaths, subpath)
			}
			sort.Strings(subpaths)
			for _, subpath := range subpaths {
				rawAccess := permission[subpath]
				access, err := parseFilesystemAccess(stringFromConfigValue(rawAccess))
				if err != nil {
					return err
				}
				entries, err := compileScopedFilesystemAccess(pathKey, subpath, access, b.pathContext)
				if err != nil {
					return err
				}
				for _, entry := range entries {
					b.addEntry(entry)
				}
			}
		}
	}
	return nil
}

func (b *permissionProfileBuilder) addEntry(entry sandbox.FileSystemSandboxEntry) {
	key := runtimeEntryKey(entry)
	if key == "" {
		return
	}
	if index, ok := b.entryIndex[key]; ok {
		b.entries[index] = entry
		return
	}
	b.entryIndex[key] = len(b.entries)
	b.entries = append(b.entries, entry)
}

func runtimeResolutionFromBuilder(profileID string, builder *permissionProfileBuilder, cwd string) (*SandboxPermissionProfileResolution, error) {
	return runtimeResolutionFromBuilderWithWorkspaceRoots(profileID, builder, cwd, nil)
}

// runtimeResolutionFromBuilderWithWorkspaceRoots materializes a managed profile
// with the supplied project-root anchor (Rust #46568).
func runtimeResolutionFromBuilderWithWorkspaceRoots(profileID string, builder *permissionProfileBuilder, cwd string, workspaceRoots []string) (*SandboxPermissionProfileResolution, error) {
	wire := runtimePermissionProfileWire{Type: "managed", Network: builder.network}
	materializedRoots, err := builder.effectiveWorkspaceRootsFor(workspaceRoots, cwd)
	if err != nil {
		return nil, err
	}
	if builder.disabled {
		wire = runtimePermissionProfileWire{Type: "disabled"}
	} else if builder.unrestrictedFilesystem {
		wire.FileSystem = &runtimeFilesystemWire{Type: "unrestricted"}
	} else {
		entries, err := builder.materializedEntriesForWorkspaceRoots(materializedRoots)
		if err != nil {
			return nil, err
		}
		wire.FileSystem = &runtimeFilesystemWire{
			Type:             "restricted",
			Entries:          entries,
			GlobScanMaxDepth: cloneIntPtrConfig(builder.globScanMaxDepth),
		}
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	raw := string(data)
	profile, err := sandbox.ParseRuntimePermissionProfileJSON(raw)
	if err != nil {
		return nil, err
	}
	return &SandboxPermissionProfileResolution{
		ID:               profileID,
		Profile:          profile,
		ProfileJSON:      raw,
		WorkspaceRoots:   materializedRoots,
		GlobScanMaxDepth: cloneIntPtrConfig(builder.globScanMaxDepth),
	}, nil
}

func (b *permissionProfileBuilder) materializedEntries(cwd string) ([]sandbox.FileSystemSandboxEntry, error) {
	roots, err := b.effectiveWorkspaceRoots(cwd)
	if err != nil {
		return nil, err
	}
	return b.materializedEntriesForWorkspaceRoots(roots)
}

func (b *permissionProfileBuilder) materializedEntriesForWorkspaceRoots(roots []string) ([]sandbox.FileSystemSandboxEntry, error) {
	var out []sandbox.FileSystemSandboxEntry
	context := b.pathContext
	if context == nil {
		return nil, fmt.Errorf("permission profile path context is required")
	}
	for _, entry := range b.entries {
		if entry.Path.Type == "special" && entry.Path.Value != nil && entry.Path.Value.Kind == "project_roots" {
			for _, root := range roots {
				subpath := ""
				if entry.Path.Value.Subpath != nil {
					subpath = *entry.Path.Value.Subpath
				}
				if _, homeRelative := context.Convention().HomeRelativeSuffix(subpath); homeRelative &&
					context.userHomeDir == nil {
					// A legacy home-relative workspace denial has no safe workspace
					// target without the owning executor's home. Clear grants.
					return []sandbox.FileSystemSandboxEntry{}, nil
				}
				path, err := resolveWorkspaceScopedPath(root, subpath, context)
				if err != nil {
					return nil, err
				}
				out = append(out, runtimePathEntryForConvention(path, entry.Access, context.Convention()))
			}
			continue
		}
		if entry.Path.Type == "glob_pattern" && strings.HasPrefix(entry.Path.Pattern, projectRootsPatternPrefix) {
			subpath := strings.TrimPrefix(entry.Path.Pattern, projectRootsPatternPrefix)
			for _, root := range roots {
				if _, homeRelative := context.Convention().HomeRelativeSuffix(subpath); homeRelative &&
					context.userHomeDir == nil {
					return []sandbox.FileSystemSandboxEntry{}, nil
				}
				if err := validateWorkspaceGlobRoot(root, context); err != nil {
					out = append(out, runtimePathEntryForConvention(root, sandbox.FileSystemAccessDeny, context.Convention()))
					continue
				}
				path, err := resolveWorkspaceScopedPath(root, subpath, context)
				if err != nil {
					return nil, err
				}
				out = append(out, runtimeGlobEntryForConvention(path, entry.Access, context.Convention()))
			}
			continue
		}
		out = append(out, entry)
	}
	return AppendFilesystemDenyReadEntries(nil, out), nil
}

func (b *permissionProfileBuilder) effectiveWorkspaceRoots(cwd string) ([]string, error) {
	return b.effectiveWorkspaceRootsFor(nil, cwd)
}

// effectiveWorkspaceRootsFor anchors the materialized project roots at the
// supplied workspace roots (Rust's
// `materialize_project_roots_with_path_uris`), falling back to the cwd when none
// are given. Configured `[permissions.<profile>.workspace_roots]` keys are
// appended in both cases.
func (b *permissionProfileBuilder) effectiveWorkspaceRootsFor(workspaceRoots []string, cwd string) ([]string, error) {
	context := b.pathContext
	if context == nil {
		var err error
		context, err = NewNativeConfigPathContext(cwd)
		if err != nil {
			return nil, err
		}
	}
	seen := map[string]bool{}
	roots := []string{}
	add := func(path string) error {
		resolved, err := resolveWorkspaceRoot(path, context)
		if err != nil {
			return err
		}
		if resolved == "" {
			return nil
		}
		key := resolved
		if uri, ok := utils.InferredPathURI(resolved); ok {
			key = uri.IdentityKey()
		}
		if seen[key] {
			return nil
		}
		seen[key] = true
		roots = append(roots, resolved)
		return nil
	}
	for _, rawRoot := range workspaceRoots {
		if err := add(strings.TrimSpace(rawRoot)); err != nil {
			return nil, err
		}
	}
	if len(roots) == 0 {
		if err := add("."); err != nil {
			return nil, err
		}
	}
	for _, rawRoot := range b.workspaceRoots {
		if !b.workspaceRootEnabled[rawRoot] {
			continue
		}
		if err := add(rawRoot); err != nil {
			return nil, err
		}
	}
	return roots, nil
}

// turnWorkspaceRootOrCWD returns the first captured workspace root, or the cwd.
func turnWorkspaceRootOrCWD(workspaceRoots []string, cwd string) string {
	for _, root := range workspaceRoots {
		if root = strings.TrimSpace(root); root != "" {
			return root
		}
	}
	return cwd
}

func compileFilesystemAccess(
	pathKey string,
	access sandbox.FileSystemAccessMode,
	context *ConfigPathContext,
) ([]sandbox.FileSystemSandboxEntry, error) {
	if context == nil {
		return nil, fmt.Errorf("permission profile path context is required")
	}
	if special, known := specialFilesystemPath(pathKey, ""); strings.HasPrefix(pathKey, ":") {
		if !known {
			return nil, nil
		}
		return []sandbox.FileSystemSandboxEntry{{Path: special, Access: access}}, nil
	}
	path := pathKey
	if containsGlobCharsForPlatform(path, context.Convention()) {
		if access == sandbox.FileSystemAccessDeny {
			resolved, err := resolveConfigGlobPattern(path, context)
			if err != nil {
				return nil, fmt.Errorf("filesystem glob path `%s`: %w", pathKey, err)
			}
			return []sandbox.FileSystemSandboxEntry{
				runtimeGlobEntryForConvention(resolved, access, context.Convention()),
			}, nil
		}
		trimmed, ok := trimTrailingGlobSubtree(path)
		if !ok {
			return nil, fmt.Errorf("filesystem glob path `%s` only supports `deny` access; use an exact path or trailing `/**` for `%s` subtree access", pathKey, access)
		}
		path = trimmed
	}
	resolved, err := resolveAbsoluteFilesystemPath(path, context)
	if err != nil {
		return nil, fmt.Errorf("filesystem path `%s`: %w", pathKey, err)
	}
	return []sandbox.FileSystemSandboxEntry{
		runtimePathEntryForConvention(resolved, access, context.Convention()),
	}, nil
}

func compileScopedFilesystemAccess(
	pathKey string,
	subpath string,
	access sandbox.FileSystemAccessMode,
	context *ConfigPathContext,
) ([]sandbox.FileSystemSandboxEntry, error) {
	if context == nil {
		return nil, fmt.Errorf("permission profile path context is required")
	}
	if subpath == "." {
		return compileFilesystemAccess(pathKey, access, context)
	}
	if err := validateRelativeSubpathForConvention(subpath, context.Convention()); err != nil {
		return nil, err
	}
	if _, homeRelative := context.Convention().HomeRelativeSuffix(subpath); homeRelative {
		if special, known := specialFilesystemPath(pathKey, subpath); known &&
			special.Value != nil && special.Value.Kind == "project_roots" {
			resolved, err := resolveAbsoluteFilesystemPath(subpath, context)
			if err != nil {
				return nil, err
			}
			return []sandbox.FileSystemSandboxEntry{
				runtimePathEntryForConvention(resolved, access, context.Convention()),
			}, nil
		}
	}
	if containsGlobCharsForPlatform(subpath, context.Convention()) && access == sandbox.FileSystemAccessDeny {
		if special, known := specialFilesystemPath(pathKey, subpath); strings.HasPrefix(pathKey, ":") {
			if !known {
				return nil, nil
			}
			if special.Value == nil || special.Value.Kind != "project_roots" {
				return nil, fmt.Errorf("filesystem path `%s` does not support nested entries", pathKey)
			}
			return []sandbox.FileSystemSandboxEntry{
				runtimeGlobEntryForConvention(projectRootsPatternPrefix+subpath, access, context.Convention()),
			}, nil
		}
		base, err := resolveAbsoluteFilesystemPath(pathKey, context)
		if err != nil {
			return nil, err
		}
		resolved, err := resolveRelativeAgainst(base, subpath, context)
		if err != nil {
			return nil, err
		}
		return []sandbox.FileSystemSandboxEntry{
			runtimeGlobEntryForConvention(resolved, access, context.Convention()),
		}, nil
	}
	if containsGlobCharsForPlatform(subpath, context.Convention()) {
		trimmed, ok := trimTrailingGlobSubtree(subpath)
		if !ok {
			return nil, fmt.Errorf("filesystem glob subpath `%s` only supports `deny` access", subpath)
		}
		subpath = trimmed
	}
	if special, known := specialFilesystemPath(pathKey, subpath); strings.HasPrefix(pathKey, ":") {
		if !known {
			return nil, nil
		}
		if special.Value == nil || special.Value.Kind == "root" || special.Value.Kind == "minimal" || special.Value.Kind == "tmpdir" || special.Value.Kind == "slash_tmp" {
			return nil, fmt.Errorf("filesystem path `%s` does not support nested entries", pathKey)
		}
		return []sandbox.FileSystemSandboxEntry{{Path: special, Access: access}}, nil
	}
	base, err := resolveAbsoluteFilesystemPath(pathKey, context)
	if err != nil {
		return nil, err
	}
	resolved, err := resolveRelativeAgainst(base, subpath, context)
	if err != nil {
		return nil, err
	}
	return []sandbox.FileSystemSandboxEntry{
		runtimePathEntryForConvention(resolved, access, context.Convention()),
	}, nil
}

func resolveAbsoluteFilesystemPath(path string, context *ConfigPathContext) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if _, homeRelative := context.Convention().HomeRelativeSuffix(path); !homeRelative {
		legacy := utils.NewLegacyAppPathString(path)
		inferred, absolute := legacy.InferAbsolutePathConvention()
		if !absolute ||
			inferred != context.Convention() &&
				!(context.Convention() == utils.ConventionWindows &&
					inferred == utils.ConventionPosix && strings.HasPrefix(path, "/")) {
			return "", fmt.Errorf("path `%s` must be absolute, use `~/...`, or start with `:`", path)
		}
	}
	return context.ResolvePathString(path)
}

func resolveConfigGlobPattern(pattern string, context *ConfigPathContext) (string, error) {
	return resolveDenyReadPatternWithContext(pattern, context)
}

func resolveRelativeAgainst(base string, subpath string, context *ConfigPathContext) (string, error) {
	baseURI, ok := utils.InferredPathURI(base)
	if !ok {
		return "", fmt.Errorf("filesystem base path `%s` is not absolute", base)
	}
	resolved, err := context.ResolveAgainst(subpath, baseURI)
	if err != nil {
		return "", err
	}
	return resolved.ToConfigPathString(context.Convention())
}

func resolveWorkspaceRoot(rawRoot string, context *ConfigPathContext) (string, error) {
	rawRoot = strings.TrimSpace(rawRoot)
	if rawRoot == "" {
		return "", nil
	}
	if strings.HasPrefix(strings.ToLower(rawRoot), "file:") {
		uri, err := utils.Parse(rawRoot)
		if err != nil {
			return "", err
		}
		if err := uri.ValidateConfigPath(context.Convention()); err != nil {
			return "", err
		}
		return uri.ToConfigPathString(context.Convention())
	}
	return context.ResolvePathString(rawRoot)
}

func resolveWorkspaceScopedPath(root string, subpath string, context *ConfigPathContext) (string, error) {
	if subpath == "" {
		return root, nil
	}
	return resolveRelativeAgainst(root, subpath, context)
}

func validateWorkspaceGlobRoot(root string, context *ConfigPathContext) error {
	uri, ok := utils.InferredPathURI(root)
	if !ok {
		return fmt.Errorf("workspace root `%s` is not an absolute path", root)
	}
	return uri.ValidateGlobDirectory(context.Convention())
}

func specialFilesystemPath(pathKey string, subpath string) (sandbox.FileSystemPath, bool) {
	switch pathKey {
	case ":root":
		return runtimeSpecialPath("root", ""), true
	case ":minimal":
		return runtimeSpecialPath("minimal", ""), true
	case ":workspace_roots", ":project_roots", ":current_working_directory":
		return runtimeSpecialPath("project_roots", subpath), true
	case ":tmpdir":
		return runtimeSpecialPath("tmpdir", ""), true
	case ":slash_tmp":
		return runtimeSpecialPath("slash_tmp", ""), true
	default:
		return sandbox.FileSystemPath{}, false
	}
}

func runtimePathEntryForConvention(
	path string,
	access sandbox.FileSystemAccessMode,
	convention utils.PathConvention,
) sandbox.FileSystemSandboxEntry {
	return sandbox.FileSystemSandboxEntry{
		Path:   sandbox.FileSystemPath{Type: "path", Path: utils.LexicalClean(path, convention)},
		Access: access,
	}
}

func runtimeGlobEntryForConvention(
	pattern string,
	access sandbox.FileSystemAccessMode,
	convention utils.PathConvention,
) sandbox.FileSystemSandboxEntry {
	if !strings.HasPrefix(pattern, projectRootsPatternPrefix) {
		pattern = utils.LexicalClean(pattern, convention)
	}
	return sandbox.FileSystemSandboxEntry{Path: sandbox.FileSystemPath{Type: "glob_pattern", Pattern: pattern}, Access: access}
}

func runtimeSpecialEntry(kind string, subpath string, access sandbox.FileSystemAccessMode) sandbox.FileSystemSandboxEntry {
	return sandbox.FileSystemSandboxEntry{Path: runtimeSpecialPath(kind, subpath), Access: access}
}

func runtimeSpecialPath(kind string, subpath string) sandbox.FileSystemPath {
	var subpathPtr *string
	if subpath != "" {
		value := subpath
		subpathPtr = &value
	}
	return sandbox.FileSystemPath{
		Type:  "special",
		Value: &sandbox.FileSystemSpecialPath{Kind: kind, Subpath: subpathPtr},
	}
}

func runtimeEntryKey(entry sandbox.FileSystemSandboxEntry) string {
	if entry.Path.Type == "special" && entry.Path.Value == nil {
		return ""
	}
	return filesystemPathKey(entry.Path)
}

func parseFilesystemAccess(raw string) (sandbox.FileSystemAccessMode, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "read":
		return sandbox.FileSystemAccessRead, nil
	case "write":
		return sandbox.FileSystemAccessWrite, nil
	case "deny", "none":
		return sandbox.FileSystemAccessDeny, nil
	default:
		return "", fmt.Errorf("unknown filesystem access %q", raw)
	}
}

func isRuntimeBuiltinPermissionProfile(profileID string) bool {
	switch profileID {
	case ":read-only", ":workspace", ":danger-full-access":
		return true
	default:
		return false
	}
}

func resolveConfigPath(path string, cwd string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if expanded, err := homedir.Expand(path); err == nil {
		path = expanded
	}
	if !filepath.IsAbs(path) && strings.TrimSpace(cwd) != "" {
		path = filepath.Join(cwd, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}

func containsGlobCharsForPlatform(path string, convention utils.PathConvention) bool {
	if convention == utils.ConventionWindows {
		if normalized, ok := utils.NormalizeWindowsDevicePath(path); ok {
			path = normalized
		}
	}
	return strings.ContainsAny(path, "*?[]")
}

func trimTrailingGlobSubtree(path string) (string, bool) {
	for _, suffix := range []string{"/**", `\**`} {
		if strings.HasSuffix(path, suffix) {
			trimmed := strings.TrimSuffix(path, suffix)
			return strings.TrimRight(trimmed, `/\`), true
		}
	}
	return "", false
}

func validateRelativeSubpathForConvention(path string, convention utils.PathConvention) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("filesystem subpath `%s` must be a descendant path without `.` or `..` components", path)
	}
	if path == "." {
		return nil
	}
	segments := utils.PathSegments(convention, path)
	if len(segments) == 0 || segments[0] == "" || segments[0] == "." || segments[0] == ".." {
		return fmt.Errorf("filesystem subpath `%s` must be a descendant path without `.` or `..` components", path)
	}
	if convention == utils.ConventionWindows && len(segments[0]) >= 2 &&
		segments[0][1] == ':' {
		return fmt.Errorf("filesystem subpath `%s` must be a descendant path without `.` or `..` components", path)
	}
	for _, segment := range segments[1:] {
		if segment == ".." {
			return fmt.Errorf("filesystem subpath `%s` must be a descendant path without `.` or `..` components", path)
		}
	}
	return nil
}

func positiveIntFromConfigValue(value any) *int {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return &v
		}
	case int64:
		if v > 0 {
			out := int(v)
			return &out
		}
	case uint64:
		if v > 0 {
			out := int(v)
			return &out
		}
	}
	return nil
}

func cloneIntPtrConfig(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
