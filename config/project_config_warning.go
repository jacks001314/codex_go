package config

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// projectGatedFeatures is the phrase Rust's TrustContext::disabled_reason_for_decision
// uses for the settings a project-trust decision gates
// (codex-rs/config/src/loader/mod.rs).
const projectGatedFeatures = "project-local config, hooks, and exec policies"

// projectConfigWarningHeader is the condensed startup warning header shared by
// codex-rs/app-server/src/lib.rs (project_config_warning) and
// codex-rs/tui/src/app/startup_prompts.rs. Rust builds the message with a
// trailing newline after the last reason, so the rendered text keeps it.
const projectConfigWarningHeader = "Project-local config, hooks, and exec policies are disabled in the following folders until the project is trusted, but skills still load.\n"

// ProjectConfigDisabledFolder is one project layer the trust decision disabled,
// mirroring the (folder, reason) pair Rust collects in project_config_warning.
type ProjectConfigDisabledFolder struct {
	Folder string
	Reason string
}

// ProjectConfigDisabledFolders collects the disabled project layers in layer
// order, lowest precedence first, the order Rust walks with
// all_layers_low_to_high.
func ProjectConfigDisabledFolders(layers []Layer) []ProjectConfigDisabledFolder {
	var disabled []ProjectConfigDisabledFolder
	for _, layer := range layers {
		if layer.Name.Type != LayerSourceProject || layer.DisabledReason == nil {
			continue
		}
		disabled = append(disabled, ProjectConfigDisabledFolder{
			Folder: layer.Name.DotCodexFolder,
			Reason: *layer.DisabledReason,
		})
	}
	return disabled
}

// ProjectConfigWarning renders Rust's project_config_warning: every project
// layer disabled by the trust decision, condensed into one message. It returns
// "" when no project layer is disabled so callers can skip the notification.
func ProjectConfigWarning(layers []Layer) string {
	disabled := ProjectConfigDisabledFolders(layers)
	if len(disabled) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(projectConfigWarningHeader)
	for index, folder := range disabled {
		fmt.Fprintf(&builder, "    %d. %s\n", index+1, folder.Folder)
		fmt.Fprintf(&builder, "       %s\n", folder.Reason)
	}
	return builder.String()
}

// ProjectConfigWarningForCWD mirrors the app-server startup warning
// (codex-rs/app-server/src/lib.rs project_config_warning) for cwd: the project
// layers visible from cwd that the project-trust decision disabled. An
// unreadable startup configuration yields no warning, matching Rust's
// best-effort startup path.
func (s *ConfigService) ProjectConfigWarningForCWD(cwd string) string {
	if s == nil {
		return ""
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	layers, err := s.projectLayersForTrustWarning(cwd)
	if err != nil {
		return ""
	}
	return ProjectConfigWarning(layers)
}

// projectLayersForTrustWarning discovers the project layers visible from cwd
// with the trust decision applied, mirroring Rust's load_project_layers: a
// project layer exists whenever the dot-codex folder is present, and the
// decision that governs it supplies a disabled reason when it is not trusted.
func (s *ConfigService) projectLayersForTrustWarning(cwd string) ([]Layer, error) {
	userPath := ConfigPath(s.codexHome)
	userValues, err := loadConfigFile(userPath)
	if err != nil {
		return nil, err
	}
	// Rust derives trust from the layers above the project (packaged defaults,
	// user, managed), so managed trust decisions are honoured here too.
	discoveryValues := map[string]any{}
	if packagedDefaults := s.packagedDefaultsLayerForRead(); packagedDefaults != nil {
		if values, ok := packagedDefaults.Config.(map[string]any); ok {
			mergeConfigMaps(discoveryValues, cloneMap(values))
		}
	}
	mergeConfigMaps(discoveryValues, cloneMap(userValues))
	for _, managed := range s.managedLayersForRead() {
		if values, ok := managed.Config.(map[string]any); ok {
			mergeConfigMaps(discoveryValues, cloneMap(values))
		}
	}

	trust := newProjectTrustContext(discoveryValues, cwd)
	codexHome := canonicalProjectPath(s.codexHome)
	var layers []Layer
	for _, dotCodexFolder := range projectDotCodexFoldersWithMarkers(cwd, projectRootMarkersFromValues(discoveryValues)) {
		// Rust never treats CODEX_HOME itself as a project layer.
		if codexHome != "" && canonicalProjectPath(dotCodexFolder) == codexHome {
			continue
		}
		layer := Layer{
			Name: LayerSource{
				Type:           LayerSourceProject,
				File:           filepath.Join(dotCodexFolder, "config.toml"),
				DotCodexFolder: dotCodexFolder,
			},
		}
		if reason := trust.disabledReasonForDir(filepath.Dir(dotCodexFolder), userPath); reason != "" {
			layer.DisabledReason = &reason
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

// projectTrustContext is the trust lookup Rust's ProjectTrustContext performs
// for project layers (codex-rs/config/src/loader/mod.rs):
//
//   - the folder's own saved decision wins;
//   - then the project-root decision;
//   - then the Git-repository decision;
//   - otherwise the folder is untrusted and the key used in messages falls back
//     to the repository root, then the project root.
type projectTrustContext struct {
	projectRootKeys []string
	repoRootKeys    []string
	projects        map[string]any
	windows         bool
}

func newProjectTrustContext(values map[string]any, cwd string) projectTrustContext {
	context := projectTrustContext{
		projectRootKeys: NormalizedProjectTrustKeys(FindProjectRoot(cwd, projectRootMarkersFromValues(values))),
		projects:        projectTrustTableFromValues(values),
		windows:         runtime.GOOS == "windows",
	}
	if repoRoot, ok := GitRootForTrust(cwd); ok {
		context.repoRootKeys = NormalizedProjectTrustKeys(repoRoot)
	}
	return context
}

// decisionForDir resolves the saved trust level governing dir and the trust key
// the disabled reason quotes. saved is false when no layer above the project
// records a decision for the folder, its project root, or its repository.
func (c projectTrustContext) decisionForDir(dir string) (trustKey string, level string, saved bool) {
	for _, lookupKey := range NormalizedProjectTrustKeys(dir) {
		if trustKey, level, ok := savedProjectTrustLevel(c.projects, lookupKey, c.windows); ok {
			return trustKey, level, true
		}
	}
	for _, lookupKey := range c.projectRootKeys {
		if trustKey, level, ok := savedProjectTrustLevel(c.projects, lookupKey, c.windows); ok {
			return trustKey, level, true
		}
	}
	for _, lookupKey := range c.repoRootKeys {
		if trustKey, level, ok := savedProjectTrustLevel(c.projects, lookupKey, c.windows); ok {
			return trustKey, level, true
		}
	}
	return c.defaultTrustKey(), "", false
}

func (c projectTrustContext) defaultTrustKey() string {
	if len(c.repoRootKeys) > 0 {
		return c.repoRootKeys[0]
	}
	if len(c.projectRootKeys) > 0 {
		return c.projectRootKeys[0]
	}
	return ""
}

// disabledReasonForDir ports Rust's TrustContext::disabled_reason_for_decision:
// a trusted folder has no reason, an explicitly untrusted folder keeps the
// stable "is marked as untrusted" prefix the remote TUI recognises, and an
// undecided folder points at the user config file.
func (c projectTrustContext) disabledReasonForDir(dir string, userConfigFile string) string {
	trustKey, level, saved := c.decisionForDir(dir)
	if saved && trustLevelIsTrusted(level) {
		return ""
	}
	if trustKey == "" {
		trustKey = dir
	}
	if saved && trustLevelIsUntrusted(level) {
		return fmt.Sprintf(
			"%s is marked as untrusted in the effective configuration. To load %s, update its trust setting. If that setting is managed by your organization, contact your administrator.",
			trustKey, projectGatedFeatures)
	}
	return fmt.Sprintf("To load %s, add %s as a trusted project in %s.", projectGatedFeatures, trustKey, userConfigFile)
}

func projectTrustTableFromValues(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	projects, _ := values["projects"].(map[string]any)
	return projects
}

// savedProjectTrustLevel resolves one lookup key the way Rust's
// project_trust_for_lookup_key does: an exact match first, then (on Windows
// only) the deterministic case-insensitive alias with the smallest spelling.
func savedProjectTrustLevel(projects map[string]any, lookupKey string, windows bool) (string, string, bool) {
	if len(projects) == 0 || strings.TrimSpace(lookupKey) == "" {
		return "", "", false
	}
	if trustKey, level, ok := projectTrustLevelForKey(projects, lookupKey); ok {
		return trustKey, level, true
	}
	if !windows {
		return "", "", false
	}
	best := ""
	for key := range projects {
		if !strings.EqualFold(key, lookupKey) {
			continue
		}
		if best == "" || key < best {
			best = key
		}
	}
	if best == "" {
		return "", "", false
	}
	if trustKey, level, ok := projectTrustLevelForKey(projects, best); ok {
		return trustKey, level, true
	}
	return "", "", false
}

func projectTrustLevelForKey(projects map[string]any, key string) (string, string, bool) {
	project, ok := projects[key].(map[string]any)
	if !ok {
		return "", "", false
	}
	level, ok := project["trust_level"].(string)
	if !ok {
		return "", "", false
	}
	return key, level, true
}

func trustLevelIsTrusted(level string) bool {
	return strings.EqualFold(strings.TrimSpace(level), "trusted")
}

func trustLevelIsUntrusted(level string) bool {
	return strings.EqualFold(strings.TrimSpace(level), "untrusted")
}
