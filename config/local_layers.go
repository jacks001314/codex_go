package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Rust parity: codex-rs/config/src/loader/local.rs. Go's effective-config loader
// (LoadWithOptions) merges every source into a single value map, so it cannot
// answer "which executor-local layers exist and what raw TOML does each hold".
// This file adds the separate, layer-preserving loader the exec-server's
// `environmentConfig/read` request needs (Rust
// codex-rs/exec-server/src/environment_config.rs::read_environment_config).

// LocalTomlLayer is one executor-local TOML source with the directory used to
// interpret its relative paths.
//
// TOML holds the document exactly as it was read: path-bearing values are
// deliberately NOT normalized, mirroring Rust's
// "Selected raw TOML. Path-bearing values have not been normalized."
type LocalTomlLayer struct {
	// Source is opaque provenance for diagnostics; behavior must not be derived
	// from it (Rust EnvironmentConfigLayer::source).
	Source string
	// BaseDir is the directory used to interpret relative paths in TOML.
	BaseDir string
	// TOML is the selected raw TOML document.
	TOML map[string]any
}

// LocalTomlLayerStack is one ordered set of executor-local TOML layers.
type LocalTomlLayerStack struct {
	// Layers are ordered from lowest to highest precedence.
	Layers []LocalTomlLayer
	// CloudInsertionIndex is the position at which a caller inserts
	// cloud-provided layers.
	CloudInsertionIndex int
}

// LocalConfigLayers is the config/requirements layer pair returned by
// environment config reads.
type LocalConfigLayers struct {
	Config       LocalTomlLayerStack
	Requirements LocalTomlLayerStack
}

// LoadLocalConfigLayers loads the fixed executor-local configuration sources
// used by environment config reads: the user layer, the trusted project layers,
// and the legacy managed layer. Cloud, selected profiles, session flags and
// thread-provided layers are not included, and project discovery uses the
// executor's user and legacy managed configuration (Rust #39306).
//
// Structural differences from Rust, recorded rather than silently dropped:
//   - Go has no system config layer (`/etc/codex/config.toml`, Windows
//     `%ProgramData%\OpenAI\Codex\config.toml`) and no system requirements
//     layer, no disk-backed MDM/enterprise-managed layer, and no macOS managed
//     admin requirements. Those layers therefore never appear here. Go's
//     ConfigService receives equivalent layers from its host at runtime, but the
//     exec-server process has no such host.
//   - Because the lowest Go layer is the user layer, CloudInsertionIndex is 0
//     ("before the user layer") where Rust uses 1 ("after its required system
//     layer"); the meaning, "cloud layers sit below every selected local
//     layer", is preserved.
//   - Rust's linked-worktree hooks layer (DiscoveredProjectLayer::
//     hooks_config_folder_override) is produced by Go's separate app-server
//     hooks discovery instead of this loader.
func LoadLocalConfigLayers(codexHome string, cwd string) (LocalConfigLayers, error) {
	codexHome = strings.TrimSpace(codexHome)
	cwd = strings.TrimSpace(cwd)

	userPath := ConfigPath(codexHome)
	userValues, _, err := loadRawLocalToml(userPath)
	if err != nil {
		return LocalConfigLayers{}, err
	}
	// Rust's load_config_toml_for_required_layer_raw returns a layer even when
	// the file is absent (an empty document), so the user layer is always part
	// of the stack.
	layers := []LocalTomlLayer{{
		Source:  formatUserLayerSource(userPath),
		BaseDir: codexHome,
		TOML:    userValues,
	}}

	// Rust #39306: legacy managed-file settings also govern the project boundary
	// and trust, so discovery uses a snapshot that includes managed values.
	discoveryValues := cloneMap(userValues)
	managedPath, managedConsulted := managedConfigPathForRuntime(codexHome)
	managedValues := map[string]any{}
	managedExists := false
	if managedConsulted {
		values, exists, err := loadRawLocalToml(managedPath)
		if err != nil {
			return LocalConfigLayers{}, err
		}
		if exists {
			managedValues = values
			managedExists = true
			mergeConfigMaps(discoveryValues, cloneMap(values))
		}
	}

	if cwd != "" && ProjectConfigEnabled(discoveryValues, cwd) {
		for _, path := range projectConfigPathsWithMarkers(cwd, projectRootMarkersFromValues(discoveryValues)) {
			values, exists, err := loadRawLocalToml(path)
			if err != nil {
				return LocalConfigLayers{}, err
			}
			if !exists {
				continue
			}
			folder := filepath.Dir(path)
			layers = append(layers, LocalTomlLayer{
				Source:  formatProjectLayerSource(folder),
				BaseDir: folder,
				TOML:    values,
			})
		}
	}

	if managedExists {
		layers = append(layers, LocalTomlLayer{
			Source:  formatLegacyManagedLayerSource(managedPath),
			BaseDir: filepath.Dir(managedPath),
			TOML:    managedValues,
		})
	}

	requirements := make([]LocalTomlLayer, 0, 2)
	requirementsPath := filepath.Join(codexHome, "requirements.toml")
	requirementsValues, requirementsExist, err := loadRawLocalToml(requirementsPath)
	if err != nil {
		return LocalConfigLayers{}, err
	}
	if requirementsExist {
		requirements = append(requirements, LocalTomlLayer{
			Source:  requirementsPath,
			BaseDir: codexHome,
			TOML:    requirementsValues,
		})
	}
	if managedExists {
		// Rust requirements_layers_from_legacy_scheme: the file-backed legacy
		// layer is composed below MDM; Go has no MDM requirements layer. The
		// legacy document only backfills approval and sandbox requirements and
		// never carries model provider policy.
		requirements = append(requirements, LocalTomlLayer{
			Source:  managedPath,
			BaseDir: filepath.Dir(managedPath),
			TOML:    LegacyManagedRequirementValues(managedValues),
		})
	}

	return LocalConfigLayers{
		Config:       LocalTomlLayerStack{Layers: layers, CloudInsertionIndex: 0},
		Requirements: LocalTomlLayerStack{Layers: requirements, CloudInsertionIndex: 0},
	}, nil
}

// Project retains only the requested TOML paths and drops layers that become
// empty (Rust LocalConfigLayers::project). An empty path selects the whole
// document; RPC boundaries reject that form when whole-document reads are not
// part of their contract.
func (l LocalConfigLayers) Project(configPaths [][]string, requirementsPaths [][]string) LocalConfigLayers {
	return LocalConfigLayers{
		Config:       l.Config.Project(configPaths),
		Requirements: l.Requirements.Project(requirementsPaths),
	}
}

// Project retains only the requested TOML paths and drops empty layers,
// adjusting the cloud insertion index for the layers that survive
// (Rust LocalTomlLayerStack::project).
func (s LocalTomlLayerStack) Project(paths [][]string) LocalTomlLayerStack {
	selector := selectorFromTOMLPaths(paths)
	projected := make([]LocalTomlLayer, 0, len(s.Layers))
	cloudInsertionIndex := 0
	for index, layer := range s.Layers {
		value, keep := projectTOMLValue(layer.TOML, selector)
		if !keep {
			continue
		}
		if index < s.CloudInsertionIndex {
			cloudInsertionIndex++
		}
		layer.TOML = value
		projected = append(projected, layer)
	}
	return LocalTomlLayerStack{Layers: projected, CloudInsertionIndex: cloudInsertionIndex}
}

// InsertSessionFlagsLayer inserts a session-flags layer carrying the given
// document above project layers but below legacy managed and MDM configuration
// (Rust environment_config.rs::include_startup_preference). The layer must be
// inserted before projection so the cloud insertion index stays consistent.
func (s LocalTomlLayerStack) InsertSessionFlagsLayer(document map[string]any, baseDir string) LocalTomlLayerStack {
	index := 0
	for index < len(s.Layers) && layerSourcePrecedence(s.Layers[index].Source) <= layerSourcePrecedence(SessionFlagsLayerSource) {
		index++
	}
	layer := LocalTomlLayer{Source: SessionFlagsLayerSource, BaseDir: baseDir, TOML: document}
	layers := make([]LocalTomlLayer, 0, len(s.Layers)+1)
	layers = append(layers, s.Layers[:index]...)
	layers = append(layers, layer)
	layers = append(layers, s.Layers[index:]...)
	if s.CloudInsertionIndex >= index {
		s.CloudInsertionIndex++
	}
	s.Layers = layers
	return s
}

// SessionFlagsLayerSource is the provenance label Rust emits for CLI-provided
// session flags (`ConfigLayerSource::SessionFlags`).
const SessionFlagsLayerSource = "session-flags"

// layerSourcePrecedence mirrors the ordering of Rust's ConfigLayerSource so a
// session-flags layer can be inserted at the same position as Rust's
// `partition_point(|layer| layer.source <= ConfigLayerSource::SessionFlags)`.
// Only the sources this loader emits need distinct values; any other label sorts
// below session flags, which matches Rust's declaration order for user and
// project layers.
func layerSourcePrecedence(source string) int16 {
	switch {
	case strings.HasPrefix(source, "user ("):
		return 20
	case strings.HasPrefix(source, "project ("):
		return 25
	case source == SessionFlagsLayerSource:
		return 30
	case strings.HasPrefix(source, "legacy managed_config.toml") || strings.HasPrefix(source, "MDM ("):
		return 40
	default:
		return 90
	}
}

// selectorFromTOMLPaths builds the projection selector for a list of TOML
// paths (Rust SelectorNode::from_paths).
func selectorFromTOMLPaths(paths [][]string) *tomlPathSelector {
	root := &tomlPathSelector{children: map[string]*tomlPathSelector{}}
	for _, path := range paths {
		node := root
		for _, segment := range path {
			child, ok := node.children[segment]
			if !ok {
				child = &tomlPathSelector{children: map[string]*tomlPathSelector{}}
				node.children[segment] = child
			}
			node = child
		}
		node.terminal = true
	}
	return root
}

type tomlPathSelector struct {
	terminal bool
	children map[string]*tomlPathSelector
}

// projectTOMLValue keeps only the selected TOML paths and reports whether the
// layer survives: a non-terminal selector that projects to an empty table drops
// the layer (Rust project_toml).
func projectTOMLValue(value map[string]any, selector *tomlPathSelector) (map[string]any, bool) {
	projected := projectTOMLValueRaw(value, selector)
	if !selector.terminal {
		if table, ok := projected.(map[string]any); ok && len(table) == 0 {
			return nil, false
		}
	}
	table, ok := projected.(map[string]any)
	if !ok {
		// A non-table document root cannot occur for a parsed TOML file; keep
		// the original document rather than inventing an empty one.
		return value, true
	}
	return table, true
}

func projectTOMLValueRaw(value any, selector *tomlPathSelector) any {
	if selector.terminal {
		return value
	}
	table, ok := value.(map[string]any)
	if !ok {
		// Preserve a non-table ancestor so it can still override lower layers.
		return value
	}
	projected := make(map[string]any, len(selector.children))
	for key, child := range table {
		childSelector, ok := selector.children[key]
		if !ok {
			continue
		}
		projected[key] = projectTOMLValueRaw(child, childSelector)
	}
	return projected
}

// loadRawLocalToml reads a TOML document without normalizing path-bearing
// values, unlike loadConfigFileIfExists which resolves relative paths for the
// effective-config pipeline. A missing file yields an empty document and false.
func loadRawLocalToml(path string) (map[string]any, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, false, nil
		}
		return nil, false, err
	}
	var values map[string]any
	if err := toml.Unmarshal(stripUTF8BOM(data), &values); err != nil {
		return nil, false, newConfigLoadError(path, err)
	}
	if values == nil {
		values = map[string]any{}
	}
	return values, true, nil
}

// BuildCLIOverridesLayer mirrors Rust codex_config::build_cli_overrides_layer:
// `-c key=value` overrides become one TOML document with dotted keys expanded.
func BuildCLIOverridesLayer(overrides []Override) map[string]any {
	values := map[string]any{}
	ApplyOverrides(values, overrides)
	return values
}

// LegacyManagedRequirementValues mirrors Rust
// loader::legacy_requirements_to_toml_value: the legacy managed_config.toml only
// backfills approval and sandbox requirements (never model provider policy).
func LegacyManagedRequirementValues(managed map[string]any) map[string]any {
	out := map[string]any{}
	if value, ok := legacyApprovalPolicyValue(managed["approval_policy"]); ok {
		out["allowed_approval_policies"] = []any{value}
	}
	if value, ok := legacyApprovalsReviewerValue(managed["approvals_reviewer"]); ok {
		reviewers := []any{value}
		if value == ApprovalsReviewerAutoReview {
			// Rust appends User so auto-review never removes the human fallback.
			reviewers = append(reviewers, ApprovalsReviewerUser)
		}
		out["allowed_approvals_reviewers"] = reviewers
	}
	if value, ok := legacySandboxModeValue(managed["sandbox_mode"]); ok {
		// Allowing read-only is a requirement for Codex to function correctly,
		// so the backfill always appends it (Rust).
		modes := []any{legacySandboxModeReadOnly}
		if value != legacySandboxModeReadOnly {
			modes = append(modes, value)
		}
		out["allowed_sandbox_modes"] = modes
	}
	return out
}

const (
	legacySandboxModeReadOnly = "read-only"
)

func legacyApprovalPolicyValue(raw any) (string, bool) {
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "untrusted", "on-failure", "on-request", "never":
		return strings.ToLower(strings.TrimSpace(text)), true
	default:
		return "", false
	}
}

func legacyApprovalsReviewerValue(raw any) (ApprovalsReviewer, bool) {
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case string(ApprovalsReviewerUser):
		return ApprovalsReviewerUser, true
	// Rust accepts `guardian_subagent` as an alias for auto_review.
	case string(ApprovalsReviewerAutoReview), "guardian_subagent":
		return ApprovalsReviewerAutoReview, true
	default:
		return "", false
	}
}

func legacySandboxModeValue(raw any) (string, bool) {
	text, ok := raw.(string)
	if !ok {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "read-only", "workspace-write", "danger-full-access", "external-sandbox":
		return strings.ToLower(strings.TrimSpace(text)), true
	default:
		return "", false
	}
}

// formatUserLayerSource mirrors Rust format_config_layer_source for
// ConfigLayerSource::User: `user (<file>)`.
func formatUserLayerSource(path string) string {
	return "user (" + path + ")"
}

// formatProjectLayerSource mirrors Rust format_config_layer_source for
// ConfigLayerSource::Project: `project (<dotCodexFolder>/config.toml)`.
func formatProjectLayerSource(dotCodexFolder string) string {
	return "project (" + filepath.Join(dotCodexFolder, "config.toml") + ")"
}

// formatLegacyManagedLayerSource mirrors Rust format_config_layer_source for
// ConfigLayerSource::LegacyManagedConfigTomlFromFile.
func formatLegacyManagedLayerSource(path string) string {
	return "legacy managed_config.toml (" + path + ")"
}
