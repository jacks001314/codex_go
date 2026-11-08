//go:build windows

package mxc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"unsafe"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/conpty"
	"golang.org/x/sys/windows"
)

// launcher session geometry. A launcher helper only needs a usable console;
// callers that care about size resize the pseudoconsole from the TTY side.
const (
	launcherSessionColumns int16 = 80
	launcherSessionRows    int16 = 24
)

// SpawnLauncherSession starts the MXC launcher helper under a pseudoconsole.
//
// This is stage one of the two-stage launch (Rust #45524): the helper is our own
// executable started with LauncherArg1, it decodes the pre-encoded request from
// its environment and performs the real PSEC launch of the workload, which is
// stage two. Only the pseudoconsole is attached here -- PROC_THREAD_ATTRIBUTE_
// PSEUDOCONSOLE plus the standard-handle triple -- and deliberately no
// PROC_THREAD_ATTRIBUTE_SECURITY_ENVIRONMENT: a PSEC child cannot receive a
// pseudoconsole, which is why the sandbox has to be entered one process deeper
// (plan sections 11 and 17).
//
// The helper is placed in a kill-on-close job at creation, mirroring Rust's
// ConPTY spawner (`utils/pty/src/win/psuedocon.rs::spawn_command`): closing the
// session then tears down the helper *and* the sandboxed workload it started,
// instead of leaving an orphaned sandboxed process behind.
func SpawnLauncherSession(executable string, args []string, env map[string]string, cwd string) (*LiveSession, error) {
	if !NativeTTYLaunchSupported() {
		// Same gate, same message as SpawnLiveSession reports for a TTY
		// request, so the capability bit and the observable behaviour cannot
		// disagree.
		return nil, errors.New(TTYUnsupportedMessage)
	}
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return nil, errors.New("MXC launcher session requires an executable")
	}
	job, err := newKillOnCloseJob()
	if err != nil {
		return nil, fmt.Errorf("MXC launcher session job: %w", err)
	}
	instance, err := conpty.Create(launcherSessionColumns, launcherSessionRows)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	abort := func(err error) (*LiveSession, error) {
		_ = instance.Close()
		_ = windows.CloseHandle(job)
		return nil, err
	}
	attributes, err := windowssandbox.NewProcThreadAttributeListWithCount(2)
	if err != nil {
		return abort(fmt.Errorf("InitializeProcThreadAttributeList: %w", err))
	}
	defer attributes.Close()
	if err := attributes.SetPseudoconsole(instance.RawHandle()); err != nil {
		return abort(fmt.Errorf("UpdateProcThreadAttribute(Pseudoconsole): %w", err))
	}
	if err := attributes.SetJob(uintptr(job)); err != nil {
		return abort(fmt.Errorf("UpdateProcThreadAttribute(JobList): %w", err))
	}
	startup := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:        uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:     windows.STARTF_USESTDHANDLES,
			StdInput:  windows.InvalidHandle,
			StdOutput: windows.InvalidHandle,
			StdErr:    windows.InvalidHandle,
		},
		ProcThreadAttributeList: attributes.WindowsList(),
	}
	commandLine, err := windows.UTF16PtrFromString(windowssandbox.ArgvToCommandLine(append([]string{executable}, args...)))
	if err != nil {
		return abort(fmt.Errorf("MXC launcher session command line: %w", err))
	}
	var workingDirectory *uint16
	if trimmed := strings.TrimSpace(cwd); trimmed != "" {
		if workingDirectory, err = windows.UTF16PtrFromString(trimmed); err != nil {
			return abort(fmt.Errorf("MXC launcher session working directory: %w", err))
		}
	}
	var environmentBlock []uint16
	var environmentPointer *uint16
	if block := windowssandbox.MakeEnvBlock(env); len(block) > 0 {
		environmentBlock = block
		environmentPointer = &environmentBlock[0]
	}

	var processInfo windows.ProcessInformation
	creationFlags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if err := windows.CreateProcess(
		nil,
		commandLine,
		nil,
		nil,
		false,
		creationFlags,
		environmentPointer,
		workingDirectory,
		&startup.StartupInfo,
		&processInfo,
	); err != nil {
		return abort(fmt.Errorf("CreateProcessW(Pseudoconsole): %w", err))
	}
	runtime.KeepAlive(attributes)
	runtime.KeepAlive(startup)
	runtime.KeepAlive(environmentBlock)
	// The pseudoconsole owns the console ends now; dropping the creation handles
	// is what lets readers observe EOF once the helper exits (Rust #45504).
	instance.FinishSpawn()

	session := &LiveSession{}
	if handle := instance.TakeInputWrite(); handle != 0 {
		// A pseudoconsole input pipe carries console input, not raw text: Rust
		// normalizes client bytes through `WindowsTtyInputNormalizer` before
		// writing them (codex-rs/utils/pty/src/windows_input.rs), so an Enter
		// key arrives as CR and backspace as DEL.
		session.Stdin = conpty.NewInputWriteCloser(os.NewFile(uintptr(handle), "mxc-launcher-stdin"))
	}
	if handle := instance.TakeOutputRead(); handle != 0 {
		session.Readers = []io.ReadCloser{os.NewFile(uintptr(handle), "mxc-launcher-stdout")}
	}
	session.wait = func() (int, error) {
		if _, err := windows.WaitForSingleObject(processInfo.Process, windows.INFINITE); err != nil {
			return -1, fmt.Errorf("waiting for the MXC launcher helper: %w", err)
		}
		var exitCode uint32
		if err := windows.GetExitCodeProcess(processInfo.Process, &exitCode); err != nil {
			return -1, fmt.Errorf("GetExitCodeProcess: %w", err)
		}
		return int(exitCode), nil
	}
	session.terminate = func() error {
		return windows.TerminateProcess(processInfo.Process, 1)
	}
	session.close = func() error {
		_ = windows.CloseHandle(processInfo.Process)
		_ = windows.CloseHandle(processInfo.Thread)
		closeErr := instance.Close()
		// Kill-on-close: dropping the job terminates whatever the helper left
		// running inside it, including the sandboxed workload.
		_ = windows.CloseHandle(job)
		return closeErr
	}
	return session, nil
}
