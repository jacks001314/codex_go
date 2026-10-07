package tool

import (
	"errors"
	"fmt"
	"strings"
)

// ResolveToolEnvironment mirrors Rust's `resolve_tool_environment`
// (codex-rs/core/src/tools/handlers/mod.rs, Rust #50741/#50962) for the
// environment-backed handlers that do not run through the shell executor:
// apply_patch, view_image and request_permissions.
//
// The returned value is the environment the call resolved to; an empty string is
// the implicit local environment. When the turn has no usable environment the
// handler reports the tool's own legacy message while `stable_environment_tools`
// is off, and the shared waiting message once the feature is on. An explicit
// selector the turn never selected is still reported as an unknown environment,
// and with the feature off an unready-but-selected environment is unknown too,
// which is exactly Rust's guard.
//
// Go apply_patch, view_image and request_permissions run against the local
// filesystem, so the implicit local environment is the only usable primary; a
// selected remote environment therefore reaches the selected-but-not-ready
// message rather than a tool-specific error, matching Rust.
// ResolveToolEnvironment is the id-only form of the resolution. Handlers that
// own an execution surface use ResolveToolEnvironmentFileSystem instead, which
// carries the selected environment's filesystem and the corrected
// implicit-primary rule.
func ResolveToolEnvironment(check *UnifiedExecEnvironmentCheck, environmentID string, legacyUnavailableMessage string) (string, error) {
	return resolveToolEnvironmentID(check, nil, environmentID, legacyUnavailableMessage)
}

// ResolveToolEnvironmentFileSystem resolves the environment a handler executes
// against and returns that environment's filesystem, mirroring Rust's
// `resolve_tool_environment` -> `turn_environment.environment.get_filesystem()`
// (codex-rs/exec-server/src/environment.rs:1171, Rust #20647 `78421face0`).
//
// The returned filesystem is the selected environment's: remote for a selected
// exec-server environment, in-process local otherwise. When no provider is
// wired, or the provider does not know the resolved id, the handler keeps its
// local filesystem rooted at `localCWD` (the pre-existing behavior).
func ResolveToolEnvironmentFileSystem(check *UnifiedExecEnvironmentCheck, provider EnvironmentFileSystemProvider, environmentID string, legacyUnavailableMessage string, localCWD string) (EnvironmentFileSystem, error) {
	resolved, err := resolveToolEnvironmentID(check, provider, environmentID, legacyUnavailableMessage)
	if err != nil {
		return nil, err
	}
	if provider != nil {
		if fileSystem, ok := provider.FileSystemFor(resolved); ok && fileSystem != nil {
			return fileSystem, nil
		}
	}
	return localEnvironmentFileSystem{cwd: strings.TrimSpace(localCWD)}, nil
}

func resolveToolEnvironmentID(check *UnifiedExecEnvironmentCheck, provider EnvironmentFileSystemProvider, environmentID string, legacyUnavailableMessage string) (string, error) {
	if check == nil {
		// The host resolved no readiness (pre-#50962 behavior), so keep the
		// historical handling instead of rejecting selectors the host never
		// modelled.
		return strings.TrimSpace(environmentID), nil
	}
	requested := strings.TrimSpace(environmentID)
	if requested == "" {
		return primaryToolEnvironmentID(check, provider, legacyUnavailableMessage)
	}
	if isLocalEnvironmentID(requested) && check.ReadyEnvironmentCount > 0 {
		return requested, nil
	}
	if provider != nil {
		if _, ok := provider.FileSystemFor(requested); ok {
			return requested, nil
		}
	}
	if !check.StableEnvironmentTools || !check.selects(requested) {
		return "", fmt.Errorf("unknown turn environment id `%s`", requested)
	}
	return "", toolEnvironmentUnavailableError(check, legacyUnavailableMessage)
}

// primaryToolEnvironmentID mirrors Rust's `environments.primary()`
// (codex-rs/core/src/environment_selection.rs:1158) = the first *ready* entry of
// `turn_environments()` in the turn's selection order. Go models the ready set as
// the ids the provider can resolve (the host-resolved usable executors) plus the
// always-usable implicit local environment.
//
// This is the fix for the pre-#20647 shortcut that returned the local id
// whenever any environment was ready: a turn whose first ready environment is
// remote now resolves to that remote
// (`view_image_routes_to_selected_remote_environment`,
// codex-rs/core/tests/suite/view_image.rs:731) instead of silently reading the
// local process filesystem.
func primaryToolEnvironmentID(check *UnifiedExecEnvironmentCheck, provider EnvironmentFileSystemProvider, legacyUnavailableMessage string) (string, error) {
	if provider == nil {
		// No execution surface is resolvable, so keep the historical answer: the
		// implicit local environment when the turn has any ready environment.
		if check.ReadyEnvironmentCount > 0 {
			return "", nil
		}
		return "", toolEnvironmentUnavailableError(check, legacyUnavailableMessage)
	}
	for _, id := range check.SelectedEnvironmentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := provider.FileSystemFor(id); ok {
			return id, nil
		}
	}
	if len(check.SelectedEnvironmentIDs) == 0 {
		if _, ok := provider.FileSystemFor(""); ok {
			return "", nil
		}
	}
	return "", toolEnvironmentUnavailableError(check, legacyUnavailableMessage)
}

// toolEnvironmentUnavailableError is Rust's message split between the tool
// legacy message and the #50741 waiting message (Rust #50962).
func toolEnvironmentUnavailableError(check *UnifiedExecEnvironmentCheck, legacyUnavailableMessage string) error {
	if check != nil && check.StableEnvironmentTools {
		return errors.New(UnifiedUnavailableEnvironmentMessage)
	}
	return errors.New(legacyUnavailableMessage)
}
