package appserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"codex_go/sandbox"
)

// EnvironmentConfigStateKind mirrors the Rust protocol
// EnvironmentConfigState variants (#38521, #38673, #38678, #38684).
type EnvironmentConfigStateKind string

const (
	// EnvironmentConfigFromThread preserves the thread-derived environment
	// configuration for this attachment.
	EnvironmentConfigFromThread EnvironmentConfigStateKind = "from_thread"
	// EnvironmentConfigPending means the owner will supply configuration later.
	EnvironmentConfigPending EnvironmentConfigStateKind = "pending"
	// EnvironmentConfigReady carries owner-supplied resolved configuration.
	EnvironmentConfigReady EnvironmentConfigStateKind = "ready"
	// EnvironmentConfigFailed means the owner could not supply configuration.
	EnvironmentConfigFailed EnvironmentConfigStateKind = "failed"
)

// EnvironmentConfig is the resolved configuration for a thread/environment
// attachment. It mirrors Rust protocol::EnvironmentConfig: the login-shell
// policy, the resolved permission profile, and the selected capability roots
// for the attachment.
type EnvironmentConfig struct {
	// AllowLoginShell reports whether shell tools may start login shells in
	// this environment.
	AllowLoginShell bool
	// PermissionProfile is the resolved profile (nil only for legacy thread
	// configs that could not resolve a profile).
	PermissionProfile *sandbox.PermissionProfile
	// ActivePermissionProfile is the named or built-in profile identity that
	// produced PermissionProfile, if any.
	ActivePermissionProfile string
	// PermissionProfileJSON is the canonical runtime profile JSON.
	PermissionProfileJSON string
	// SelectedCapabilityRoots are the capability roots selected for this
	// thread's environment attachment.
	SelectedCapabilityRoots []SelectedCapabilityRoot
}

// EnvironmentConfigState is the configuration supplied for a thread's selected
// environment attachment.
type EnvironmentConfigState struct {
	Kind   EnvironmentConfigStateKind
	Config *EnvironmentConfig
	Error  string
}

// EnvironmentConfigOrigin records whether a resolved attachment configuration
// follows later thread setting updates (Thread) or is owned by the attachment
// owner (Owner). Mirrors Rust EnvironmentConfigOrigin (#38678).
type EnvironmentConfigOrigin string

const (
	EnvironmentConfigOriginThread EnvironmentConfigOrigin = "thread"
	EnvironmentConfigOriginOwner  EnvironmentConfigOrigin = "owner"
)

// DefaultEnvironmentConfigState returns the attachment form that inherits the
// thread's environment configuration, matching Rust's app-server boundary
// where requests always resolve to EnvironmentConfigState::FromThread.
func DefaultEnvironmentConfigState() EnvironmentConfigState {
	return EnvironmentConfigState{Kind: EnvironmentConfigFromThread}
}

// resolveEnvironmentConfig resolves FromThread selections to the thread
// configuration and records who owns future updates, mirroring Rust
// resolve_selection_config. Owner states are returned unchanged.
func resolveEnvironmentConfig(selection map[string]any, threadConfig *EnvironmentConfig) (EnvironmentConfigState, EnvironmentConfigOrigin) {
	state, err := environmentConfigStateFromAnyMap(selection)
	if err != nil || state.Kind == EnvironmentConfigFromThread {
		if threadConfig == nil {
			return EnvironmentConfigState{Kind: EnvironmentConfigFromThread}, EnvironmentConfigOriginThread
		}
		config := cloneEnvironmentConfig(threadConfig)
		return EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: config}, EnvironmentConfigOriginThread
	}
	return state, EnvironmentConfigOriginOwner
}

// environmentConfigFromAnyMap parses a selection's `config` value into an
// EnvironmentConfigState. The value may be nil (FromThread), a state map
// {"state": "ready", "config": {...}, "error": "..."}, or a ready config map
// directly (treated as Ready for compatibility with owner callbacks).
func environmentConfigStateFromAnyMap(selection map[string]any) (EnvironmentConfigState, error) {
	if selection == nil {
		return DefaultEnvironmentConfigState(), nil
	}
	value, present := selection["config"]
	if !present || value == nil {
		return DefaultEnvironmentConfigState(), nil
	}
	state, err := environmentConfigStateFromAny(value)
	if err != nil {
		return EnvironmentConfigState{}, err
	}
	return state, nil
}

func environmentConfigStateFromAny(value any) (EnvironmentConfigState, error) {
	if value == nil {
		return DefaultEnvironmentConfigState(), nil
	}
	if object, ok := value.(map[string]any); ok {
		if rawState, present := object["state"]; present && rawState != nil {
			kind := EnvironmentConfigStateKind(strings.ToLower(strings.TrimSpace(stringFromAny(rawState))))
			switch kind {
			case EnvironmentConfigFromThread:
				return DefaultEnvironmentConfigState(), nil
			case EnvironmentConfigPending:
				return EnvironmentConfigState{Kind: EnvironmentConfigPending}, nil
			case EnvironmentConfigReady:
				config, err := environmentConfigFromAny(object["config"])
				if err != nil {
					return EnvironmentConfigState{}, err
				}
				if config == nil {
					return EnvironmentConfigState{}, fmt.Errorf("environment configuration state `ready` requires a configuration")
				}
				return EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: config}, nil
			case EnvironmentConfigFailed:
				return EnvironmentConfigState{Kind: EnvironmentConfigFailed, Error: strings.TrimSpace(stringFromAny(object["error"]))}, nil
			default:
				return EnvironmentConfigState{}, fmt.Errorf("unknown environment configuration state `%s`", rawState)
			}
		}
		// A bare config object (no state key) is treated as Ready.
		config, err := environmentConfigFromAny(object)
		if err != nil {
			return EnvironmentConfigState{}, err
		}
		return EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: config}, nil
	}
	return EnvironmentConfigState{}, fmt.Errorf("environment configuration must be an object")
}

func environmentConfigFromAny(value any) (*EnvironmentConfig, error) {
	if value == nil {
		return nil, nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("environment configuration must be an object")
	}
	config := &EnvironmentConfig{}
	if raw, present := object["allow_login_shell"]; present {
		config.AllowLoginShell = boolFromAny(raw)
	} else if raw, present := object["allowLoginShell"]; present {
		config.AllowLoginShell = boolFromAny(raw)
	}
	config.ActivePermissionProfile = strings.TrimSpace(firstNonEmpty(
		stringFromAny(object["permission_profile_id"]),
		stringFromAny(object["permissionProfileId"]),
	))
	profileJSON := strings.TrimSpace(firstNonEmpty(
		stringFromAny(object["permission_profile"]),
		stringFromAny(object["permissionProfile"]),
	))
	if profileJSON == "" {
		if raw, present := object["permission_profile_json"]; present {
			profileJSON = strings.TrimSpace(stringFromAny(raw))
		} else if raw, present := object["permissionProfileJSON"]; present {
			profileJSON = strings.TrimSpace(stringFromAny(raw))
		}
	}
	if profileJSON != "" {
		profile, err := sandbox.ParseRuntimePermissionProfileJSON(profileJSON)
		if err != nil {
			return nil, fmt.Errorf("invalid environment permission profile: %w", err)
		}
		config.PermissionProfile = profile
		config.PermissionProfileJSON = profileJSON
	}
	if raw, present := object["selected_capability_roots"]; present {
		roots, err := selectedCapabilityRootsFromAny(raw)
		if err != nil {
			return nil, err
		}
		config.SelectedCapabilityRoots = roots
	} else if raw, present := object["selectedCapabilityRoots"]; present {
		roots, err := selectedCapabilityRootsFromAny(raw)
		if err != nil {
			return nil, err
		}
		config.SelectedCapabilityRoots = roots
	}
	return config, nil
}

func selectedCapabilityRootsFromAny(value any) ([]SelectedCapabilityRoot, error) {
	items, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]SelectedCapabilityRoot); typedOK {
			return cloneSelectedCapabilityRoots(typed), nil
		}
		return nil, fmt.Errorf("selected capability roots must be an array")
	}
	out := make([]SelectedCapabilityRoot, 0, len(items))
	for _, item := range items {
		raw, err := json.Marshal(item)
		if err != nil {
			return nil, err
		}
		var root SelectedCapabilityRoot
		if err := json.Unmarshal(raw, &root); err != nil {
			return nil, fmt.Errorf("invalid selected capability root: %w", err)
		}
		out = append(out, root)
	}
	return out, nil
}

// environmentConfigToAny serializes a resolved EnvironmentConfig for storage in
// a selection map or thread metadata.
func environmentConfigToAny(config *EnvironmentConfig) map[string]any {
	if config == nil {
		return nil
	}
	profileJSON := strings.TrimSpace(config.PermissionProfileJSON)
	if profileJSON == "" && config.PermissionProfile != nil {
		if raw, err := sandbox.RuntimePermissionProfileJSON(*config.PermissionProfile); err == nil {
			profileJSON = raw
		}
	}
	roots := make([]any, 0, len(config.SelectedCapabilityRoots))
	for _, root := range config.SelectedCapabilityRoots {
		roots = append(roots, map[string]any{
			"id": root.ID,
			"location": map[string]any{
				"type":          string(root.Location.Type),
				"environmentId": root.Location.EnvironmentID,
				"path":          root.Location.Path,
			},
		})
	}
	out := map[string]any{
		"allow_login_shell":       config.AllowLoginShell,
		"selectedCapabilityRoots": roots,
	}
	if profileJSON != "" {
		out["permission_profile"] = profileJSON
	}
	if config.ActivePermissionProfile != "" {
		out["permission_profile_id"] = config.ActivePermissionProfile
	}
	return out
}

func environmentConfigStateToAny(state EnvironmentConfigState) map[string]any {
	switch state.Kind {
	case EnvironmentConfigPending:
		return map[string]any{"state": string(EnvironmentConfigPending)}
	case EnvironmentConfigReady:
		return map[string]any{"state": string(EnvironmentConfigReady), "config": environmentConfigToAny(state.Config)}
	case EnvironmentConfigFailed:
		return map[string]any{"state": string(EnvironmentConfigFailed), "error": state.Error}
	default:
		return map[string]any{"state": string(EnvironmentConfigFromThread)}
	}
}

// validateEnvironmentConfigForSelection enforces the attachment-config
// contract shared by selection validation and owner callbacks, mirroring Rust
// validate_environment_config (#38521).
func validateEnvironmentConfigForSelection(environmentID string, config *EnvironmentConfig) error {
	if config == nil {
		return nil
	}
	if len(config.SelectedCapabilityRoots) > maxSelectedCapabilityRoots {
		return fmt.Errorf("environment readiness contains more than %d selected capability roots", maxSelectedCapabilityRoots)
	}
	seen := make(map[string]struct{}, len(config.SelectedCapabilityRoots))
	for _, root := range config.SelectedCapabilityRoots {
		if strings.TrimSpace(root.ID) == "" || root.Location.Type != CapabilityRootLocationEnvironment || root.Location.EnvironmentID != environmentID {
			return fmt.Errorf("selected capability roots must have unique non-empty IDs and belong to environment `%s`", environmentID)
		}
		if _, dup := seen[root.ID]; dup {
			return fmt.Errorf("selected capability roots must have unique non-empty IDs and belong to environment `%s`", environmentID)
		}
		seen[root.ID] = struct{}{}
	}
	return nil
}

// validateEnvironmentSelectionConfig validates the parsed configuration state
// for one selection, returning the normalized state map. Ready configurations
// must satisfy the capability-root contract; Pending and Failed states are
// accepted for attachment lifecycle support (#38684).
func validateEnvironmentSelectionConfig(environmentID string, selection map[string]any) (map[string]any, error) {
	state, err := environmentConfigStateFromAnyMap(selection)
	if err != nil {
		return nil, err
	}
	switch state.Kind {
	case EnvironmentConfigReady:
		if err := validateEnvironmentConfigForSelection(environmentID, state.Config); err != nil {
			return nil, err
		}
	case EnvironmentConfigFailed:
		if strings.TrimSpace(state.Error) == "" {
			return nil, fmt.Errorf("environment configuration state `failed` requires an error message")
		}
	}
	return environmentConfigStateToAny(state), nil
}

// selectionEnvironmentID returns the environment id of a selection map using
// both accepted key spellings.
func selectionEnvironmentID(selection map[string]any) string {
	return strings.TrimSpace(firstNonEmpty(
		threadItemStringFromAnyMap(selection, "environmentId"),
		threadItemStringFromAnyMap(selection, "environment_id"),
	))
}

func cloneEnvironmentConfig(config *EnvironmentConfig) *EnvironmentConfig {
	if config == nil {
		return nil
	}
	clone := *config
	if config.PermissionProfile != nil {
		profile := *config.PermissionProfile
		clone.PermissionProfile = &profile
	}
	clone.SelectedCapabilityRoots = cloneSelectedCapabilityRoots(config.SelectedCapabilityRoots)
	return &clone
}

// environmentConfigPermissionProfile returns the resolved profile of a ready
// attachment config, or nil when absent.
func environmentConfigPermissionProfile(config *EnvironmentConfig) *sandbox.PermissionProfile {
	if config == nil {
		return nil
	}
	return config.PermissionProfile
}
