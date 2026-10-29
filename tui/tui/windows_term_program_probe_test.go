package tui

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Mirrors Rust #45811's windows_term_program_tests.rs: only a successful probe
// answer counts, and a probe that outlives the deadline is killed and reaped.

// fakeTermProgramProcess stands in for Rust's std::process::Child so the
// bounded launch, wait, and reap behaviour can be exercised on every host.
type fakeTermProgramProcess struct {
	output  []byte
	waitErr error

	// release is closed by Kill to unblock a WaitOutput that models a slow probe.
	release  chan struct{}
	killOnce sync.Once
	killed   atomic.Bool
	reaped   chan struct{}
}

func newFakeTermProgramProcess(output []byte, waitErr error, blockWait bool) *fakeTermProgramProcess {
	process := &fakeTermProgramProcess{
		output:  output,
		waitErr: waitErr,
		reaped:  make(chan struct{}),
	}
	if blockWait {
		process.release = make(chan struct{})
	}
	return process
}

func (p *fakeTermProgramProcess) Kill() error {
	p.killed.Store(true)
	if p.release != nil {
		p.killOnce.Do(func() { close(p.release) })
	}
	return nil
}

func (p *fakeTermProgramProcess) WaitOutput() ([]byte, error) {
	if p.release != nil {
		<-p.release
	}
	close(p.reaped)
	return p.output, p.waitErr
}

func (p *fakeTermProgramProcess) waitReaped(t *testing.T) {
	t.Helper()
	select {
	case <-p.reaped:
	case <-time.After(5 * time.Second):
		t.Fatal("timed-out terminal probe was not reaped")
	}
}

// TestWindowsTermProgramHelperProcess backs exitErrorFor: the test binary is
// re-executed to produce a real *exec.ExitError with a chosen exit status.
func TestWindowsTermProgramHelperProcess(t *testing.T) {
	code := os.Getenv("CODEX_TERM_PROGRAM_HELPER_EXIT")
	if code == "" {
		return
	}
	status, err := strconv.Atoi(code)
	if err != nil {
		os.Exit(3)
	}
	os.Exit(status)
}

func exitErrorFor(t *testing.T, code int) error {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestWindowsTermProgramHelperProcess$")
	command.Env = append(os.Environ(), "CODEX_TERM_PROGRAM_HELPER_EXIT="+strconv.Itoa(code))
	if _, err := command.Output(); err != nil {
		return err
	}
	t.Fatalf("helper exited 0, want %d", code)
	return nil
}

func readFakeTermProgram(t *testing.T, process *fakeTermProgramProcess, timeout time.Duration) VscodeDetection {
	t.Helper()
	termProgram, present, ok := readWindowsTermProgramWithTimeout(
		func() (windowsTermProgramProcess, error) { return process, nil },
		timeout,
	)
	return ReadWindowsVscodeDetection(func() (string, bool, bool) { return termProgram, present, ok })
}

func TestReadWindowsTermProgramOnlyUsesSuccessfulProbe(t *testing.T) {
	exitOne := exitErrorFor(t, 1)
	exitTwo := exitErrorFor(t, 2)

	for _, testCase := range []struct {
		name   string
		output string
		err    error
		want   VscodeDetection
	}{
		{name: "vscode", output: "TERM_PROGRAM=vscode\r\n", want: VscodeDetectionVsCode},
		{name: "vscode then exit 1", output: "TERM_PROGRAM=vscode\r\n", err: exitOne, want: VscodeDetectionOther},
		{name: "other terminal", output: "TERM_PROGRAM=WindowsTerminal", want: VscodeDetectionOther},
		{name: "absent", err: exitOne, want: VscodeDetectionOther},
		{name: "unexpected exit", err: exitTwo, want: VscodeDetectionUnknown},
		{name: "empty value", output: "TERM_PROGRAM=\r\nTERM_PROGRAM=vscode\r\n", want: VscodeDetectionOther},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			process := newFakeTermProgramProcess([]byte(testCase.output), testCase.err, false)
			if got := readFakeTermProgram(t, process, 5*time.Second); got != testCase.want {
				t.Fatalf("detection = %q, want %q", got, testCase.want)
			}
			if process.killed.Load() {
				t.Fatal("a probe that answered before the deadline must not be killed")
			}
			process.waitReaped(t)
		})
	}
}

func TestReadWindowsTermProgramFailedLaunchIsUnknown(t *testing.T) {
	termProgram, present, ok := readWindowsTermProgramWithTimeout(
		func() (windowsTermProgramProcess, error) { return nil, exec.ErrNotFound },
		5*time.Second,
	)
	if termProgram != "" || present || ok {
		t.Fatalf("failed launch = (%q, %v, %v), want inconclusive", termProgram, present, ok)
	}
}

func TestReadWindowsTermProgramTimesOutAndReapsRunningProbe(t *testing.T) {
	process := newFakeTermProgramProcess([]byte("TERM_PROGRAM=vscode\r\n"), nil, true)
	started := time.Now()
	detection := readFakeTermProgram(t, process, 100*time.Millisecond)
	if detection != VscodeDetectionUnknown {
		t.Fatalf("timed-out detection = %q, want unknown", detection)
	}
	if !KeyboardEnhancementDisabledFor(nil, true, detection) {
		t.Fatal("an inconclusive WSL probe must keep keyboard enhancement disabled")
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("probe deadline took %v, want < 2s", elapsed)
	}
	process.waitReaped(t)
	if !process.killed.Load() {
		t.Fatal("timed-out probe must be killed")
	}
}

func TestReadWindowsTermProgramTimesOutDuringLaunchAndReapsLateChild(t *testing.T) {
	process := newFakeTermProgramProcess([]byte("TERM_PROGRAM=vscode\r\n"), nil, false)
	release := make(chan struct{})
	started := time.Now()
	detection := func() VscodeDetection {
		termProgram, present, ok := readWindowsTermProgramWithTimeout(
			func() (windowsTermProgramProcess, error) {
				// Model a WSL launch that cannot return a child until interop recovers.
				<-release
				return process, nil
			},
			100*time.Millisecond,
		)
		return ReadWindowsVscodeDetection(func() (string, bool, bool) { return termProgram, present, ok })
	}()
	close(release)
	if detection != VscodeDetectionUnknown {
		t.Fatalf("detection during blocked launch = %q, want unknown", detection)
	}
	if !KeyboardEnhancementDisabledFor(nil, true, detection) {
		t.Fatal("an inconclusive WSL probe must keep keyboard enhancement disabled")
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("blocked launch took %v, want < 2s", elapsed)
	}
	process.waitReaped(t)
	if !process.killed.Load() {
		t.Fatal("a child that appears after the deadline must be killed")
	}
}

func TestKeyboardEnhancementAutoDisablesForVscodeOrUnknownInWSL(t *testing.T) {
	for _, detection := range []VscodeDetection{
		VscodeDetectionVsCode,
		VscodeDetectionOther,
		VscodeDetectionUnknown,
	} {
		got := KeyboardEnhancementDisabledFor(nil, true, detection)
		if want := detection != VscodeDetectionOther; got != want {
			t.Fatalf("isWSL detection=%q disabled = %v, want %v", detection, got, want)
		}
		if KeyboardEnhancementDisabledFor(nil, false, detection) {
			t.Fatalf("non-WSL detection=%q must not disable keyboard enhancement", detection)
		}
	}
}

func TestKeyboardEnhancementEnvFlagOverridesAutoDetection(t *testing.T) {
	disabled := "0"
	enabled := "1"
	for _, detection := range []VscodeDetection{
		VscodeDetectionVsCode,
		VscodeDetectionOther,
		VscodeDetectionUnknown,
	} {
		if KeyboardEnhancementDisabledFor(&disabled, true, detection) {
			t.Fatalf("disable=0 detection=%q must force the enhancement on", detection)
		}
		if !KeyboardEnhancementDisabledFor(&enabled, false, detection) {
			t.Fatalf("disable=1 detection=%q must force the enhancement off", detection)
		}
	}
}
