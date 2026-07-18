package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"codex_go/sandbox"

	json "github.com/goccy/go-json"
	"github.com/mitchellh/go-homedir"
)

const projectRootsPatternPrefix = "codex-project-roots://"

type SandboxPermissionProfileResolution struct {
	ID             string
	Profile        *sandbox.PermissionProfile
	ProfileJSON    string
	WorkspaceRoots []string
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
	globScanMaxDepth       *int
	network                sandbox.NetworkAccess
	disabled               bool
	unrestrictedFilesystem bool
	workspaceRoots         []string
	workspaceRootEnabled   map[string]bool
}

func (c *Config) ResolveSandboxPermissionProfile(profileID string, cwd string) (*SandboxPermissionProfileResolution, error) {
	if c == nil {
		c = &Config{Values: map[string]any{}}
	}
	profiles := permissionProfilesFromConfig(c.Values["permissions"])
	if err := validateConfigPermissionProfileNames(profiles); err != nil {
		return nil, err
	}
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		profileID = stringFromConfigValue(c.Values["default_permissions"])
	}
	if profileID == "" {
		if len(profiles) > 0 {
			return nil, fmt.Errorf("config defines `[permissions]` profiles but does not set `default_permissions`")
		}
		return c.resolveLegacySandboxPermissionProfile(cwd)
	}
	if !strings.HasPrefix(profileID, ":") {
		if _, ok := profiles[profileID]; ok {
			builder, err := compileConfigPermissionProfile(profiles, profileID, map[string]bool{})
			if err != nil {
				return nil, err
			}
			return runtimeResolutionFromBuilder(profileID, builder, cwd)
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
	if values == nil {
		values = map[string]any{}
	}
	profiles := permissionProfilesFromConfig(values["permissions"])
	if err := validateConfigPermissionProfileNames(profiles); err != nil {
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
		summaries = append(summaries, sandbox.PermissionProfileSummary{
			ID:          name,
			Description: description,
			Allowed:     true,
		})
	}
	return summaries, nil
}

func (c *Config) resolveLegacySandboxPermissionProfile(cwd string) (*SandboxPermissionProfileResolution, error) {
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
		applyLegacyWorkspaceWriteConfig(&profile, c.Values["sandbox_workspace_write"], cwd)
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

func compileConfigPermissionProfile(profiles map[string]any, profileID string, stack map[string]bool) (*permissionProfileBuilder, error) {
	if strings.HasPrefix(profileID, ":") {
		if isRuntimeBuiltinPermissionProfile(profileID) {
			return builtinPermissionProfileBuilder(profileID)
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
	builder := newPermissionProfileBuilder()
	if parent := stringFromConfigValue(profile["extends"]); parent != "" {
		parentBuilder, err := compileConfigPermissionProfile(profiles, parent, stack)
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

func newPermissionProfileBuilder() *permissionProfileBuilder {
	return &permissionProfileBuilder{
		entryIndex:           map[string]int{},
		network:              sandbox.NetworkRestricted,
		workspaceRootEnabled: map[string]bool{},
	}
}

func builtinPermissionProfileBuilder(profileID string) (*permissionProfileBuilder, error) {
	builder := newPermissionProfileBuilder()
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
		builder.addEntry(runtimeSpecialEntry("project_roots", ".codex", sandbox.FileSystemAccessRead))
	default:
		return nil, fmt.Errorf("default_permissions refers to unknown built-in profile `%s`", profileID)
	}
	return builder, nil
}

func (b *permissionProfileBuilder) clone() *permissionProfileBuilder {
	clone := newPermissionProfileBuilder()
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
	for rawPath, rawEnabled := range values {
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
	for pathKey, rawPermission := range values {
		if pathKey == "glob_scan_max_depth" {
			continue
		}
		switch permission := rawPermission.(type) {
		case string:
			access, err := parseFilesystemAccess(permission)
			if err != nil {
				return err
			}
			entries, err := compileFilesystemAccess(pathKey, access)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				b.addEntry(entry)
			}
		case map[string]any:
			for subpath, rawAccess := range permission {
				access, err := parseFilesystemAccess(stringFromConfigValue(rawAccess))
				if err != nil {
					return err
				}
				entries, err := compileScopedFilesystemAccess(pathKey, subpath, access)
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
	wire := runtimePermissionProfileWire{Type: "managed", Network: builder.network}
	workspaceRoots, err := builder.effectiveWorkspaceRoots(cwd)
	if err != nil {
		return nil, err
	}
	if builder.disabled {
		wire = runtimePermissionProfileWire{Type: "disabled"}
	} else if builder.unrestrictedFilesystem {
		wire.FileSystem = &runtimeFilesystemWire{Type: "unrestricted"}
	} else {
		entries, err := builder.materializedEntriesForWorkspaceRoots(workspaceRoots)
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
	return &SandboxPermissionProfileResolution{ID: profileID, Profile: profile, ProfileJSON: raw, WorkspaceRoots: workspaceRoots}, nil
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
	for _, entry := range b.entries {
		if entry.Path.Type == "special" && entry.Path.Value != nil && entry.Path.Value.Kind == "project_roots" {
			for _, root := range roots {
				path := root
				if entry.Path.Value.Subpath != nil && *entry.Path.Value.Subpath != "" {
					path = filepath.Join(root, *entry.Path.Value.Subpath)
				}
				out = append(out, runtimePathEntry(path, entry.Access))
			}
			continue
		}
		if entry.Path.Type == "glob_pattern" && strings.HasPrefix(entry.Path.Pattern, projectRootsPatternPrefix) {
			subpath := strings.TrimPrefix(entry.Path.Pattern, projectRootsPatternPrefix)
			for _, root := range roots {
				out = append(out, runtimeGlobEntry(filepath.Join(root, subpath), entry.Access))
			}
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}

func (b *permissionProfileBuilder) effectiveWorkspaceRoots(cwd string) ([]string, error) {
	root := resolveConfigPath(".", cwd)
	if root == "" {
		workingDir, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		root = filepath.Clean(workingDir)
	}
	seen := map[string]bool{root: true}
	roots := []string{root}
	for _, rawRoot := range b.workspaceRoots {
		if !b.workspaceRootEnabled[rawRoot] {
			continue
		}
		root := resolveConfigPath(rawRoot, cwd)
		if root != "" && !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	return roots, nil
}

func compileFilesystemAccess(pathKey string, access sandbox.FileSystemAccessMode) ([]sandbox.FileSystemSandboxEntry, error) {
	if special, known := specialFilesystemPath(pathKey, ""); strings.HasPrefix(pathKey, ":") {
		if !known {
			return nil, nil
		}
		return []sandbox.FileSystemSandboxEntry{{Path: special, Access: access}}, nil
	}
	path := pathKey
	if containsGlobChars(path) {
		if access == sandbox.FileSystemAccessDeny {
			if !filepath.IsAbs(path) {
				return nil, fmt.Errorf("filesystem glob path `%s` must be absolute", pathKey)
			}
			return []sandbox.FileSystemSandboxEntry{runtimeGlobEntry(filepath.Clean(path), access)}, nil
		}
		trimmed, ok := trimTrailingGlobSubtree(path)
		if !ok {
			return nil, fmt.Errorf("filesystem glob path `%s` only supports `deny` access; use an exact path or trailing `/**` for `%s` subtree access", pathKey, access)
		}
		path = trimmed
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("filesystem path `%s` must be absolute or a recognized special path", pathKey)
	}
	return []sandbox.FileSystemSandboxEntry{runtimePathEntry(filepath.Clean(path), access)}, nil
}

func compileScopedFilesystemAccess(pathKey string, subpath string, access sandbox.FileSystemAccessMode) ([]sandbox.FileSystemSandboxEntry, error) {
	if subpath == "." {
		return compileFilesystemAccess(pathKey, access)
	}
	if err := validateRelativeSubpath(subpath); err != nil {
		return nil, err
	}
	if containsGlobChars(subpath) && access == sandbox.FileSystemAccessDeny {
		if special, known := specialFilesystemPath(pathKey, subpath); strings.HasPrefix(pathKey, ":") {
			if !known {
				return nil, nil
			}
			if special.Value == nil || special.Value.Kind != "project_roots" {
				return nil, fmt.Errorf("filesystem path `%s` does not support nested entries", pathKey)
			}
			return []sandbox.FileSystemSandboxEntry{runtimeGlobEntry(projectRootsPatternPrefix+subpath, access)}, nil
		}
		if !filepath.IsAbs(pathKey) {
			return nil, fmt.Errorf("filesystem path `%s` must be absolute or a recognized special path", pathKey)
		}
		return []sandbox.FileSystemSandboxEntry{runtimeGlobEntry(filepath.Join(pathKey, subpath), access)}, nil
	}
	if containsGlobChars(subpath) {
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
	if !filepath.IsAbs(pathKey) {
		return nil, fmt.Errorf("filesystem path `%s` must be absolute or a recognized special path", pathKey)
	}
	return []sandbox.FileSystemSandboxEntry{runtimePathEntry(filepath.Join(pathKey, subpath), access)}, nil
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

func runtimePathEntry(path string, access sandbox.FileSystemAccessMode) sandbox.FileSystemSandboxEntry {
	return sandbox.FileSystemSandboxEntry{Path: sandbox.FileSystemPath{Type: "path", Path: filepath.Clean(path)}, Access: access}
}

func runtimeGlobEntry(pattern string, access sandbox.FileSystemAccessMode) sandbox.FileSystemSandboxEntry {
	if !strings.HasPrefix(pattern, projectRootsPatternPrefix) {
		pattern = filepath.Clean(pattern)
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
	switch entry.Path.Type {
	case "path":
		return "path:" + filepath.Clean(entry.Path.Path)
	case "glob_pattern":
		return "glob:" + filepath.Clean(entry.Path.Pattern)
	case "special":
		if entry.Path.Value == nil {
			return ""
		}
		subpath := ""
		if entry.Path.Value.Subpath != nil {
			subpath = *entry.Path.Value.Subpath
		}
		return "special:" + entry.Path.Value.Kind + ":" + subpath
	default:
		return ""
	}
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

func containsGlobChars(path string) bool {
	return strings.ContainsAny(path, "*?[")
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

func validateRelativeSubpath(path string) error {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return fmt.Errorf("filesystem subpath `%s` must be a descendant path without `.` or `..` components", path)
	}
	if path == "." {
		return nil
	}
	for _, part := range strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "" || part == "." || part == ".." {
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
