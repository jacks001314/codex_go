package execserver

import (
	"fmt"
	"strings"

	"codex_go/sandbox/windowssandbox"
	"codex_go/utils"
)

// Windows sandbox level wire values (Rust WindowsSandboxLevel, serde
// rename_all = "kebab-case").
const (
	windowsSandboxLevelDisabled        = "disabled"
	windowsSandboxLevelRestrictedToken = "restricted-token"
	windowsSandboxLevelElevated        = "elevated"
	windowsSandboxLevelMxc             = "mxc"
)

// parseWindowsSandboxLevelValue validates the level the way Rust's enum
// deserialization does; an empty value keeps Go's legacy default.
//
// The wire shape is platform-neutral - Rust deserializes
// `windows_sandbox_level` on every platform - so the validation lives here and
// both the Windows launcher and the ordinary launch path use it.
func parseWindowsSandboxLevelValue(value string) (string, error) {
	level := strings.TrimSpace(value)
	switch level {
	case "", windowsSandboxLevelDisabled, windowsSandboxLevelRestrictedToken,
		windowsSandboxLevelElevated, windowsSandboxLevelMxc:
		return level, nil
	default:
		return "", fmt.Errorf("unknown variant `%s`, expected one of `disabled`, `restricted-token`, `elevated`, `mxc`", value)
	}
}

// WindowsSandboxSelection is the executor's concrete Windows sandbox choice.
// MXC is a backend selection rather than a restricted-token level (Rust
// #45730).
type WindowsSandboxSelection string

const (
	WindowsSandboxSelectionDisabled        WindowsSandboxSelection = "disabled"
	WindowsSandboxSelectionRestrictedToken WindowsSandboxSelection = "restricted-token"
	WindowsSandboxSelectionElevated        WindowsSandboxSelection = "elevated"
	WindowsSandboxSelectionMXC             WindowsSandboxSelection = "mxc"
)

// selectWindowsSandboxForExecutor resolves the wire level for the executor's
// path convention. Non-Windows paths never select a Windows sandbox backend.
func selectWindowsSandboxForExecutor(value string, cwd string) (WindowsSandboxSelection, error) {
	level, err := parseWindowsSandboxLevelValue(value)
	if err != nil {
		return "", err
	}
	if !executorPathUsesWindowsConvention(cwd) {
		return WindowsSandboxSelectionDisabled, nil
	}
	switch level {
	case windowsSandboxLevelMxc:
		return WindowsSandboxSelectionMXC, nil
	case windowsSandboxLevelElevated:
		return WindowsSandboxSelectionElevated, nil
	case windowsSandboxLevelDisabled:
		return WindowsSandboxSelectionDisabled, nil
	default:
		// Empty keeps the historical restricted-token default for Windows
		// executor paths.
		return WindowsSandboxSelectionRestrictedToken, nil
	}
}

func (s WindowsSandboxSelection) restrictedTokenLevel() windowssandbox.WindowsSandboxLevel {
	if s == WindowsSandboxSelectionElevated {
		return windowssandbox.WindowsSandboxLevelElevated
	}
	return windowssandbox.WindowsSandboxLevelRestrictedToken
}

func executorPathUsesWindowsConvention(cwd string) bool {
	cwd = strings.TrimSpace(cwd)
	if path, err := utils.Parse(cwd); err == nil {
		if convention, ok := path.InferConvention(); ok {
			return convention == utils.ConventionWindows
		}
	}
	legacy := utils.NewLegacyAppPathString(cwd)
	convention, ok := legacy.InferAbsolutePathConvention()
	return ok && convention == utils.ConventionWindows
}
