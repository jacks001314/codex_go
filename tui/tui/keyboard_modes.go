package tui

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codex_go/envutil"
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

func KeyboardEnhancementDisabledFor(disableEnv *string, isWSL bool, detection VscodeDetection) bool {
	if value, ok := ParseBoolEnv(disableEnv); ok {
		return value
	}
	// VS Code running a WSL shell can hide TERM_PROGRAM from the Linux process
	// environment. If the Windows-side probe is inconclusive, avoid enabling the
	// keyboard mode that can break dead-key composition in VS Code on WSL.
	return isWSL && (detection == VscodeDetectionVsCode || detection == VscodeDetectionUnknown)
}

// KeyboardEnhancementDisabled mirrors Rust keyboard_enhancement_disabled: the
// explicit environment flag wins, otherwise only a WSL host disables the
// enhancement, and only for a VS Code or inconclusive reading.
func KeyboardEnhancementDisabled() bool {
	isWSL := codextui.IsProbablyWSL()
	detection := VscodeDetectionOther
	if isWSL {
		detection = DetectVscodeTerminal()
	}
	return KeyboardEnhancementDisabledFor(disableKeyboardEnhancementEnvVar(), isWSL, detection)
}

// disableKeyboardEnhancementEnvVar reports the override as a nullable string so
// an unset variable and an unparsable one both fall through to detection.
func disableKeyboardEnhancementEnvVar() *string {
	value, ok := os.LookupEnv(DisableKeyboardEnhancementEnvVar)
	if !ok || value == "" {
		return nil
	}
	return &value
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
// probe under the one-second deadline. Only a timeout, a launch failure, or an
// unexpected exit status is inconclusive; exit status 1 and an empty variable
// both mean the Windows session does not export TERM_PROGRAM.
func ProbeWindowsTermProgram() (string, bool, bool) {
	return readWindowsTermProgramWithTimeout(spawnWindowsTermProgram, WindowsTermProgramProbeTimeout)
}

// windowsTermProgramProcess is the probe child as the bounded reader sees it.
// *exec.Cmd cannot be faked, so this mirrors Rust's std::process::Child seam and
// keeps launch, timeout, and reap behaviour testable on every host.
type windowsTermProgramProcess interface {
	// Kill terminates the process. It may race with WaitOutput.
	Kill() error
	// WaitOutput waits for exit, reaps the child, and returns its stdout.
	WaitOutput() ([]byte, error)
}

type execWindowsTermProgramProcess struct {
	command *exec.Cmd
	stdout  *bytes.Buffer
}

func (p execWindowsTermProgramProcess) Kill() error {
	if p.command.Process == nil {
		return errors.New("windows TERM_PROGRAM probe has not started")
	}
	return p.command.Process.Kill()
}

func (p execWindowsTermProgramProcess) WaitOutput() ([]byte, error) {
	err := p.command.Wait()
	return p.stdout.Bytes(), err
}

// spawnWindowsTermProgram launches the probe. Start happens here because a WSL
// interop launch can block before the child exists at all.
func spawnWindowsTermProgram() (windowsTermProgramProcess, error) {
	command := exec.Command("cmd.exe", "/d", "/s", "/c", "set TERM_PROGRAM")
	// Rust #48483: the probe is piped, so it must not allocate a console window
	// when the TUI itself has no console.
	envutil.SuppressConsoleWindow(command)
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Start(); err != nil {
		return nil, err
	}
	return execWindowsTermProgramProcess{command: command, stdout: &stdout}, nil
}

// readWindowsTermProgramWithTimeout mirrors Rust
// read_windows_vscode_detection_with_timeout. WSL interop can block inside the
// launch itself, so launching, waiting, and cleanup all run off the startup
// thread under a single deadline; a child that only appears after the deadline
// is still killed and reaped rather than leaked.
func readWindowsTermProgramWithTimeout(
	spawn func() (windowsTermProgramProcess, error),
	timeout time.Duration,
) (string, bool, bool) {
	if spawn == nil {
		return "", false, false
	}
	type outcome struct {
		termProgram string
		present     bool
		ok          bool
	}
	deadline := time.Now().Add(timeout)
	done := make(chan outcome, 1)
	go func() {
		process, err := spawn()
		if err != nil || process == nil {
			done <- outcome{}
			return
		}
		if !time.Now().Before(deadline) {
			_ = process.Kill()
			_, _ = process.WaitOutput()
			done <- outcome{}
			return
		}
		var expired atomic.Bool
		timer := time.AfterFunc(time.Until(deadline), func() {
			expired.Store(true)
			_ = process.Kill()
		})
		output, waitErr := process.WaitOutput()
		timer.Stop()
		if expired.Load() {
			done <- outcome{}
			return
		}
		termProgram, present, ok := parseWindowsTermProgramOutput(output, waitErr)
		done <- outcome{termProgram: termProgram, present: present, ok: ok}
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case result := <-done:
		return result.termProgram, result.present, result.ok
	case <-timer.C:
		return "", false, false
	}
}

// parseWindowsTermProgramOutput mirrors Rust's outcome mapping. Only the first
// TERM_PROGRAM line counts, and an empty value means the session does not
// export one rather than an inconclusive probe.
func parseWindowsTermProgramOutput(output []byte, waitErr error) (string, bool, bool) {
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			// `cmd.exe /c set TERM_PROGRAM` exits with 1 when the variable is absent.
			return "", false, true
		}
		return "", false, false
	}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSuffix(line, "\r")
		value, found := strings.CutPrefix(line, "TERM_PROGRAM=")
		if !found {
			continue
		}
		if strings.TrimSpace(value) == "" {
			return "", false, true
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
