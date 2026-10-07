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
func ResolveToolEnvironment(check *UnifiedExecEnvironmentCheck, environmentID string, legacyUnavailableMessage string) (string, error) {
	if check == nil {
		// The host resolved no readiness (pre-#50962 behavior), so keep the
		// historical handling instead of rejecting selectors the host never
		// modelled.
		return strings.TrimSpace(environmentID), nil
	}
	requested := strings.TrimSpace(environmentID)
	if requested == "" {
		if check.ReadyEnvironmentCount > 0 {
			return "", nil
		}
		return "", toolEnvironmentUnavailableError(check, legacyUnavailableMessage)
	}
	if isLocalEnvironmentID(requested) && check.ReadyEnvironmentCount > 0 {
		return requested, nil
	}
	if !check.StableEnvironmentTools || !check.selects(requested) {
		return "", fmt.Errorf("unknown turn environment id `%s`", requested)
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
