package execserver

import (
	"fmt"
	"strings"
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
