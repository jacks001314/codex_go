package tui

import "strings"

// Rust parity subset: codex-rs/tui/src/tui/keyboard_modes.rs.

const DisableKeyboardEnhancementEnvVar = "CODEX_TUI_DISABLE_KEYBOARD_ENHANCEMENT"

type KeyboardMode string

const (
	KeyboardModeNormal KeyboardMode = "normal"
	KeyboardModePaste  KeyboardMode = "paste"
)

func ParseBoolEnv(value *string) (bool, bool) {
	if value == nil {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(*value)) {
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	default:
		return false, false
	}
}

func KeyboardEnhancementDisabledFor(disableEnv *string, isWSL bool, isVSCodeTerminal bool) bool {
	if value, ok := ParseBoolEnv(disableEnv); ok {
		return value
	}
	return isWSL && isVSCodeTerminal
}

func VscodeTerminalDetected(linuxTermProgram *string, windowsTermProgram *string) bool {
	return termProgramIsVSCode(linuxTermProgram) || termProgramIsVSCode(windowsTermProgram)
}

func TmuxSessionDetected(tmux *string, tmuxPane *string) bool {
	return tmux != nil || tmuxPane != nil
}

func TmuxShouldEnableModifyOtherKeysFor(runningInTmuxSession bool, extendedKeysFormat *string) bool {
	return runningInTmuxSession && extendedKeysFormat != nil && *extendedKeysFormat == "csi-u"
}

func ResetKeyboardEnhancementFlagsANSI() string {
	return "\x1b[<u"
}

func EnableModifyOtherKeysANSI() string {
	return "\x1b[>4;2m"
}

func DisableModifyOtherKeysANSI() string {
	return "\x1b[>4;0m"
}

func termProgramIsVSCode(value *string) bool {
	return value != nil && strings.EqualFold(*value, "vscode")
}
