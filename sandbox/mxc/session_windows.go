//go:build windows

package mxc

import (
	"fmt"
	"io"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// LiveSession is a running MXC sandboxed process with streamed stdio, for
// callers that need a session handle instead of a run-to-completion launch
// (exec-server's process API).
type LiveSession struct {
	Stdin   io.WriteCloser
	Readers []io.ReadCloser

	wait      func() (int, error)
	terminate func() error
	close     func() error

	closeOnce sync.Once
	closeErr  error
}

// Wait blocks until the sandboxed process exits and returns its exit code.
func (s *LiveSession) Wait() (int, error) {
	if s == nil || s.wait == nil {
		return -1, fmt.Errorf("invalid MXC live session")
	}
	return s.wait()
}

// Terminate stops the sandboxed process.
func (s *LiveSession) Terminate() error {
	if s == nil || s.terminate == nil {
		return nil
	}
	return s.terminate()
}

// Close releases stdio and native handles. Safe to call more than once.
func (s *LiveSession) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		if s.Stdin != nil {
			_ = s.Stdin.Close()
		}
		for _, reader := range s.Readers {
			if reader != nil {
				_ = reader.Close()
			}
		}
		if s.close != nil {
			s.closeErr = s.close()
		}
	})
	return s.closeErr
}

// SpawnLiveSession starts the prepared command inside a PSEC sandbox and returns
// streamed stdio. It fails closed exactly like launchNative: unsupported policy
// features or native errors never fall back to an unsandboxed process.
//
// This entry point only serves pipe-backed sessions. TTY requests go through
// SpawnLauncherSession's two-stage path instead, because a PSEC child cannot
// receive a pseudoconsole (plan sections 11 and 17); dropping the `tty`
// parameter is deliberate, so a caller cannot ask this path for a console and
// have it silently ignored.
func SpawnLiveSession(request *NativeRequest) (*LiveSession, error) {
	if request == nil {
		return nil, fmt.Errorf("invalid MXC launcher request")
	}
	support := nativeLaunchSupport()
	if !support.Available {
		return nil, fmt.Errorf("%s: %s", NativeUnavailableMessage, support.Reason)
	}
	if err := validateLaunchablePolicy(request.Policy, support); err != nil {
		return nil, err
	}
	spec, err := BuildSecurityEnvironmentSpec(request.Policy.securityEnvironmentSpec())
	if err != nil {
		return nil, fmt.Errorf("build process security environment: %w", err)
	}
	environment, err := createSecurityEnvironment(spec)
	if err != nil {
		return nil, err
	}
	defer environment.Close()

	stdinRead, stdinWrite, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		_ = stdinRead.Close()
		_ = stdinWrite.Close()
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}
	for _, handle := range []uintptr{stdinRead.Fd(), stdoutWrite.Fd(), stderrWrite.Fd()} {
		if err := windows.SetHandleInformation(windows.Handle(handle), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return nil, fmt.Errorf("mark inherited handle: %w", err)
		}
	}

	startup, err := newSecurityEnvironmentStartupInfo(environment.handle, false)
	if err != nil {
		return nil, err
	}
	defer startup.Close()
	startup.startup.Flags |= windows.STARTF_USESTDHANDLES
	startup.startup.StdInput = windows.Handle(stdinRead.Fd())
	startup.startup.StdOutput = windows.Handle(stdoutWrite.Fd())
	startup.startup.StdErr = windows.Handle(stderrWrite.Fd())

	commandLine, err := windows.UTF16PtrFromString(request.ScriptCode)
	if err != nil {
		return nil, fmt.Errorf("MXC command line: %w", err)
	}
	var workingDirectory *uint16
	if request.WorkingDirectory != "" {
		if workingDirectory, err = windows.UTF16PtrFromString(request.WorkingDirectory); err != nil {
			return nil, fmt.Errorf("MXC working directory: %w", err)
		}
	}
	environmentBlock, err := buildEnvironmentBlock(request.Env)
	if err != nil {
		return nil, err
	}
	var environmentPointer *uint16
	if len(environmentBlock) > 0 {
		environmentPointer = &environmentBlock[0]
	}

	var processInfo windows.ProcessInformation
	creationFlags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	if err := windows.CreateProcess(nil, commandLine, nil, nil, true, creationFlags, environmentPointer, workingDirectory, &startup.startup.StartupInfo, &processInfo); err != nil {
		return nil, fmt.Errorf("CreateProcessW(SecurityEnvironment): %w", err)
	}
	// The child owns the other ends of the pipes now.
	_ = stdinRead.Close()
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()

	job, err := newSandboxJob(request.Policy)
	if err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		_ = windows.CloseHandle(processInfo.Process)
		_ = windows.CloseHandle(processInfo.Thread)
		return nil, err
	}
	if err := windows.AssignProcessToJobObject(job, processInfo.Process); err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		_ = windows.CloseHandle(job)
		_ = windows.CloseHandle(processInfo.Process)
		_ = windows.CloseHandle(processInfo.Thread)
		return nil, fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	_, resumeErr := windows.ResumeThread(processInfo.Thread)

	session := &LiveSession{
		Stdin:   stdinWrite,
		Readers: []io.ReadCloser{stdoutRead, stderrRead},
	}
	session.wait = func() (int, error) {
		if _, err := windows.WaitForSingleObject(processInfo.Process, windows.INFINITE); err != nil {
			return -1, fmt.Errorf("waiting for the MXC command: %w", err)
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
		_ = windows.CloseHandle(job)
		_ = windows.CloseHandle(processInfo.Process)
		_ = windows.CloseHandle(processInfo.Thread)
		return nil
	}
	if resumeErr != nil {
		_ = session.Close()
		return nil, fmt.Errorf("ResumeThread: %w", resumeErr)
	}
	return session, nil
}
