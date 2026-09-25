package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	codextui "codex_go/tui"
)

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

// VscodeDetection is Rust's three-state terminal detection (#48118). `other`
// means a conclusive non-VS-Code reading, `unknown` an inconclusive one (a
// non-Linux host or a WSL host whose Windows-side probe could not answer).
type VscodeDetection string

const (
	VscodeDetectionVsCode  VscodeDetection = "vscode"
	VscodeDetectionOther   VscodeDetection = "other"
	VscodeDetectionUnknown VscodeDetection = "unknown"
)

// WindowsTermProgramProbe reports the Windows-side TERM_PROGRAM inherited by a
// WSL process. present distinguishes an answered probe whose variable is absent
// (`cmd.exe /c set TERM_PROGRAM` exits 1) from a failed or timed-out probe.
type WindowsTermProgramProbe func() (termProgram string, present bool, ok bool)

const WindowsTermProgramProbeTimeout = time.Second

// VscodeTerminalDetection mirrors Rust vscode_terminal_detected: a conclusive
// Linux-side reading wins, otherwise the Windows-side detection answers.
func VscodeTerminalDetection(linuxTermProgram *string, windowsDetection VscodeDetection) VscodeDetection {
	if termProgramIsVSCode(linuxTermProgram) {
		return VscodeDetectionVsCode
	}
	return windowsDetection
}

// WindowsVscodeDetection mirrors Rust windows_vscode_detection: only a Linux
// host can see a Windows TERM_PROGRAM (through WSL), and a Linux host that is
// not running under WSL is conclusively `other`.
func WindowsVscodeDetection(goos string, isWSL bool, probe WindowsTermProgramProbe) VscodeDetection {
	if goos != "linux" {
		return VscodeDetectionUnknown
	}
	if !isWSL {
		return VscodeDetectionOther
	}
	return ReadWindowsVscodeDetection(probe)
}

// ReadWindowsVscodeDetection mirrors Rust
// read_windows_vscode_detection_with_timeout's outcome mapping.
func ReadWindowsVscodeDetection(probe WindowsTermProgramProbe) VscodeDetection {
	if probe == nil {
		return VscodeDetectionUnknown
	}
	termProgram, present, ok := probe()
	switch {
	case !ok:
		return VscodeDetectionUnknown
	case !present:
		return VscodeDetectionOther
	case termProgramIsVSCode(&termProgram):
		return VscodeDetectionVsCode
	default:
		return VscodeDetectionOther
	}
}

var (
	windowsVscodeDetectionOnce  sync.Once
	windowsVscodeDetectionValue VscodeDetection
)

// DetectVscodeTerminal mirrors Rust detect_vscode_terminal. The Windows-side
// probe result is cached for the process lifetime, matching Rust's OnceLock: a
// timeout is remembered instead of retried on the startup thread.
func DetectVscodeTerminal() VscodeDetection {
	termProgram := termProgramEnv()
	windowsVscodeDetectionOnce.Do(func() {
		windowsVscodeDetectionValue = WindowsVscodeDetection(
			runtime.GOOS,
			codextui.IsProbablyWSL(),
			ProbeWindowsTermProgram,
		)
	})
	return VscodeTerminalDetection(termProgram, windowsVscodeDetectionValue)
}

// ProbeWindowsTermProgram runs Rust's `cmd.exe /d /s /c "set TERM_PROGRAM"`
// probe with a one-second deadline. Only a one-second timeout (or a spawn
// failure) is inconclusive; exit status 1 and an empty variable both mean the
// Windows session does not export TERM_PROGRAM.
func ProbeWindowsTermProgram() (string, bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), WindowsTermProgramProbeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", "set TERM_PROGRAM")
	output, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", false, false
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", false, true
		}
		return "", false, false
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		value, ok := strings.CutPrefix(line, "TERM_PROGRAM=")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		return value, true, true
	}
	return "", false, true
}

// termProgramEnv reports TERM_PROGRAM as a nullable string so an unset variable
// and an empty one are indistinguishable, matching Rust's env::var().ok().
func termProgramEnv() *string {
	value, ok := os.LookupEnv("TERM_PROGRAM")
	if !ok || value == "" {
		return nil
	}
	return &value
}

// SetVscodeDetectionForTest installs a cached Windows-side detection for a test
// and returns a restore function, mirroring Rust's OnceLock seam.
func SetVscodeDetectionForTest(detection VscodeDetection) func() {
	windowsVscodeDetectionOnce.Do(func() {})
	previous := windowsVscodeDetectionValue
	windowsVscodeDetectionValue = detection
	return func() {
		windowsVscodeDetectionValue = previous
	}
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
