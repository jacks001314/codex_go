//go:build windows

package tea

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
	"codex_go/sandbox/windowssandbox/conpty"
	codextui "codex_go/tui"
)

const realPTYTerminalRestoreEnv = "CODEX_GO_TUI_PTY_SMOKE"
const realPTYTerminalRestoreChildEnv = "CODEX_GO_TUI_PTY_CHILD"
const windowsStatusDLLInitFailed uint32 = 0xC0000142
const windowsStatusDLLInitFailedSigned = -1073741502

func TestRealPTYTerminalRestoreSmoke(t *testing.T) {
	if os.Getenv(realPTYTerminalRestoreEnv) != "1" {
		t.Skipf("set %s=1 to run the Windows terminal restore smoke", realPTYTerminalRestoreEnv)
	}
	runWindowsPTYTerminalRestoreParent(t)
}

func TestMain(m *testing.M) {
	if os.Getenv(realPTYTerminalRestoreChildEnv) == "1" {
		if err := runWindowsPTYTerminalRestoreChild(); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "run TUI in ConPTY child: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runWindowsPTYTerminalRestoreChild() error {
	input := os.Stdin
	if conin, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		defer func() { _ = conin.Close() }()
		input = conin
	}
	output := os.Stdout
	if conout, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		defer func() { _ = conout.Close() }()
		output = conout
	}
	_, _ = output.Write([]byte("CODEX_PTY_CHILD_READY\r\n"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := Run(ctx, codextui.NewState(nil), Options{Width: 80, Height: 24}, input, output)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func runWindowsPTYTerminalRestoreParent(t *testing.T) {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	skipIfWindowsConPTYOutputUnavailable(t, cwd)
	restoreEnv := setWindowsPTYTerminalRestoreChildEnv()
	defer restoreEnv()
	created, instance, err := spawnCurrentUserConPTY(
		[]string{os.Args[0], "-test.run", "^TestRealPTYTerminalRestoreSmoke$", "-test.v"},
		cwd,
		80,
		24,
	)
	if err != nil {
		t.Fatalf("spawn ConPTY child: %v", err)
	}
	defer func() { _ = created.Close() }()
	defer func() { _ = instance.Close() }()

	var output lockedTerminalOutput
	outputRead := instance.TakeOutputRead()
	outputDone, err := windowssandbox.ReadHandleLoop(outputRead, func(chunk []byte) {
		output.Write(chunk)
	})
	if err != nil {
		t.Fatalf("read ConPTY output: %v", err)
	}

	entered, exitCode, exited, err := waitForOutputContainsOrExit(created, &output, "\x1b[?1049h", 3*time.Second)
	if err != nil {
		_ = windowssandbox.TerminateCreatedProcess(created, 1)
		t.Fatalf("wait for ConPTY child output: %v", err)
	}
	if !entered {
		if exited {
			skipIfWindowsConPTYHostDLLInitFailed(t, exitCode, output.String())
			t.Fatalf("ConPTY child exited before entering alternate screen with code %d; output=%q", exitCode, output.String())
		}
		_ = windowssandbox.TerminateCreatedProcess(created, 1)
		t.Fatalf("ConPTY child did not enter alternate screen; output=%q", output.String())
	}
	inputWrite := windows.Handle(instance.TakeInputWrite())
	var written uint32
	if err := windows.WriteFile(inputWrite, []byte{0x03}, &written, nil); err != nil {
		t.Fatalf("send Ctrl+C to ConPTY child: %v", err)
	}
	_ = windows.CloseHandle(inputWrite)

	timeoutMS := int64(5000)
	outcome, err := windowssandbox.WaitCreatedProcess(created, &timeoutMS, windowssandbox.CancellationToken{})
	if err != nil {
		t.Fatalf("wait ConPTY child: %v", err)
	}
	if outcome != windowssandbox.ProcessWaitExited {
		_ = windowssandbox.TerminateCreatedProcess(created, 1)
		t.Fatalf("ConPTY child wait outcome = %s; output=%q", outcome, output.String())
	}
	exitCode, err = windowssandbox.CreatedProcessExitCode(created)
	if err != nil {
		t.Fatalf("read ConPTY child exit code: %v", err)
	}
	skipIfWindowsConPTYHostDLLInitFailed(t, exitCode, output.String())
	if exitCode != 0 {
		t.Fatalf("ConPTY child exit code = %d; output=%q", exitCode, output.String())
	}
	_ = instance.Close()

	select {
	case <-outputDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ConPTY output drain")
	}
	text := output.String()
	if !strings.Contains(text, "\x1b[?1049h") {
		t.Fatalf("ConPTY output did not enter alternate screen; output=%q", text)
	}
	if !strings.Contains(text, "\x1b[?1049l") {
		t.Fatalf("ConPTY output did not leave alternate screen; output=%q", text)
	}
}

func setWindowsPTYTerminalRestoreChildEnv() func() {
	type previousValue struct {
		value string
		ok    bool
	}
	keys := map[string]string{
		realPTYTerminalRestoreChildEnv: "1",
		realPTYTerminalRestoreEnv:      "1",
	}
	previous := make(map[string]previousValue, len(keys))
	for key, value := range keys {
		old, ok := os.LookupEnv(key)
		previous[key] = previousValue{value: old, ok: ok}
		_ = os.Setenv(key, value)
	}
	return func() {
		for key, old := range previous {
			if old.ok {
				_ = os.Setenv(key, old.value)
				continue
			}
			_ = os.Unsetenv(key)
		}
	}
}

func spawnCurrentUserConPTY(command []string, cwd string, columns int16, rows int16) (*windowssandbox.CreatedProcess, *conpty.Instance, error) {
	if len(command) == 0 {
		return nil, nil, windowssandbox.ErrInvalidRequest
	}
	instance, err := conpty.Create(columns, rows)
	if err != nil {
		return nil, nil, err
	}
	attributeList, err := windowssandbox.NewProcThreadAttributeListWithCount(1)
	if err != nil {
		_ = instance.Close()
		return nil, nil, err
	}
	defer attributeList.Close()
	if err := attributeList.SetPseudoconsole(instance.RawHandle()); err != nil {
		_ = instance.Close()
		return nil, nil, err
	}
	desktop, err := windowssandbox.PrepareLaunchDesktop(false)
	if err != nil {
		_ = instance.Close()
		return nil, nil, err
	}
	applicationName, err := windows.UTF16PtrFromString(command[0])
	if err != nil {
		_ = desktop.Close()
		_ = instance.Close()
		return nil, nil, err
	}
	commandLine, err := windows.UTF16FromString(windowssandbox.ArgvToCommandLine(command))
	if err != nil {
		_ = desktop.Close()
		_ = instance.Close()
		return nil, nil, err
	}
	cwdPtr, err := windows.UTF16PtrFromString(cwd)
	if err != nil {
		_ = desktop.Close()
		_ = instance.Close()
		return nil, nil, err
	}
	startupInfo := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{
			Cb:        uint32(unsafe.Sizeof(windows.StartupInfoEx{})),
			Flags:     windows.STARTF_USESTDHANDLES,
			StdInput:  windows.InvalidHandle,
			StdOutput: windows.InvalidHandle,
			StdErr:    windows.InvalidHandle,
			Desktop:   desktop.StartupInfoDesktop(),
		},
		ProcThreadAttributeList: attributeList.WindowsList(),
	}
	var processInfo windows.ProcessInformation
	creationFlags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT)
	err = windows.CreateProcess(
		applicationName,
		&commandLine[0],
		nil,
		nil,
		false,
		creationFlags,
		nil,
		cwdPtr,
		&startupInfo.StartupInfo,
		&processInfo,
	)
	runtime.KeepAlive(attributeList)
	runtime.KeepAlive(commandLine)
	if err != nil {
		_ = desktop.Close()
		_ = instance.Close()
		return nil, nil, err
	}
	return &windowssandbox.CreatedProcess{
		ProcessHandle: uintptr(processInfo.Process),
		ThreadHandle:  uintptr(processInfo.Thread),
		ProcessID:     processInfo.ProcessId,
		ThreadID:      processInfo.ThreadId,
		StartupFlags:  startupInfo.StartupInfo.Flags,
		Desktop:       desktop,
	}, instance, nil
}

func skipIfWindowsConPTYOutputUnavailable(t *testing.T, cwd string) {
	t.Helper()
	const probe = "CODEX_PTY_OUTPUT_PROBE"
	cmdPath := os.Getenv("ComSpec")
	if strings.TrimSpace(cmdPath) == "" {
		cmdPath = "cmd.exe"
	}
	output, exitCode, outcome, err := captureWindowsConPTYCommand(
		[]string{cmdPath, "/d", "/c", "echo " + probe},
		cwd,
		time.Second,
	)
	if err != nil {
		t.Skipf("host ConPTY output probe failed: %v", err)
	}
	if outcome != windowssandbox.ProcessWaitExited {
		t.Skipf("host ConPTY output probe did not exit cleanly: outcome=%s output=%q", outcome, output)
	}
	skipIfWindowsConPTYHostDLLInitFailed(t, exitCode, output)
	if exitCode != 0 {
		t.Skipf("host ConPTY output probe exited with code %d; output=%q", exitCode, output)
	}
	if !strings.Contains(output, probe) {
		t.Skipf("host ConPTY output probe produced no readable terminal output; output=%q", output)
	}
}

func captureWindowsConPTYCommand(command []string, cwd string, timeout time.Duration) (string, int, windowssandbox.ProcessWaitOutcome, error) {
	created, instance, err := spawnCurrentUserConPTY(command, cwd, 80, 24)
	if err != nil {
		return "", 0, "", err
	}
	defer func() { _ = created.Close() }()
	defer func() { _ = instance.Close() }()
	var output lockedTerminalOutput
	outputDone, err := windowssandbox.ReadHandleLoop(instance.TakeOutputRead(), func(chunk []byte) {
		output.Write(chunk)
	})
	if err != nil {
		return "", 0, "", err
	}
	timeoutMS := int64(timeout / time.Millisecond)
	outcome, err := windowssandbox.WaitCreatedProcess(created, &timeoutMS, windowssandbox.CancellationToken{})
	if err != nil {
		return output.String(), 0, "", err
	}
	exitCode := 1
	if outcome == windowssandbox.ProcessWaitExited {
		exitCode, err = windowssandbox.CreatedProcessExitCode(created)
		if err != nil {
			return output.String(), 0, outcome, err
		}
	} else {
		_ = windowssandbox.TerminateCreatedProcess(created, 1)
	}
	_ = instance.Close()
	select {
	case <-outputDone:
	case <-time.After(time.Second):
	}
	return output.String(), exitCode, outcome, nil
}

func waitForOutputContainsOrExit(process *windowssandbox.CreatedProcess, output *lockedTerminalOutput, needle string, timeout time.Duration) (bool, int, bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if output.Contains(needle) {
			return true, 0, false, nil
		}
		exitCode, exited, err := createdProcessExitCodeIfExited(process)
		if err != nil {
			return false, 0, false, err
		}
		if exited {
			return false, exitCode, true, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	if output.Contains(needle) {
		return true, 0, false, nil
	}
	exitCode, exited, err := createdProcessExitCodeIfExited(process)
	if err != nil {
		return false, 0, false, err
	}
	return false, exitCode, exited, nil
}

func createdProcessExitCodeIfExited(process *windowssandbox.CreatedProcess) (int, bool, error) {
	timeoutMS := int64(0)
	outcome, err := windowssandbox.WaitCreatedProcess(process, &timeoutMS, windowssandbox.CancellationToken{})
	if err != nil {
		return 0, false, err
	}
	if outcome != windowssandbox.ProcessWaitExited {
		return 0, false, nil
	}
	exitCode, err := windowssandbox.CreatedProcessExitCode(process)
	if err != nil {
		return 0, false, err
	}
	return exitCode, true, nil
}

func skipIfWindowsConPTYHostDLLInitFailed(t *testing.T, exitCode int, output string) {
	t.Helper()
	if uint32(exitCode) == windowsStatusDLLInitFailed {
		t.Skipf("host ConPTY child exited with STATUS_DLL_INIT_FAILED (%d), matching the Rust legacy ConPTY CI limitation; output=%q", windowsStatusDLLInitFailedSigned, output)
	}
}

type lockedTerminalOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *lockedTerminalOutput) Write(chunk []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.data = append(o.data, chunk...)
}

func (o *lockedTerminalOutput) Contains(needle string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Contains(string(o.data), needle)
}

func (o *lockedTerminalOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.data)
}
