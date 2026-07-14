//go:build windows

package tea

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/internal/sandbox/windowssandbox"
	"codex_go/internal/sandbox/windowssandbox/conpty"
)

const slashParityEnv = "CODEX_GO_SLASH_PARITY"

func TestSystemCodexSlashParityWithConPTY(t *testing.T) {
	if os.Getenv(slashParityEnv) != "1" {
		t.Skipf("set %s=1 to run system Codex vs local code.exe slash parity through ConPTY", slashParityEnv)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	root := filepath.Clean(filepath.Join(cwd, "..", "..", ".."))
	localExe := filepath.Join(root, "code.exe")
	systemExe := filepath.Join(os.Getenv("APPDATA"), "npm", "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe")
	for _, path := range []string{localExe, systemExe} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("required executable %s: %v", path, err)
		}
	}

	commands := []string{"/help", "/status", "/mcp", "/exit"}
	system := runCodexSlashPTY(t, systemExe, root, commands)
	local := runCodexSlashPTY(t, localExe, root, commands)

	for _, want := range []string{
		"OpenAI Codex",
		"/help",
		"/status",
		"/mcp",
		"model",
		"approval",
		"sandbox",
	} {
		if !strings.Contains(strings.ToLower(system), strings.ToLower(want)) {
			t.Fatalf("system output missing %q:\n%s", want, system)
		}
		if !strings.Contains(strings.ToLower(local), strings.ToLower(want)) {
			t.Fatalf("local output missing %q:\n%s", want, local)
		}
	}
}

func runCodexSlashPTY(t *testing.T, exe string, cwd string, commands []string) string {
	t.Helper()
	home := t.TempDir()
	env := map[string]string{
		"CODEX_HOME":     home,
		"OPENAI_API_KEY": "sk-test",
		"TERM":           "xterm-256color",
	}
	created, instance, err := spawnCurrentUserConPTYWithEnv([]string{exe, "--no-alt-screen"}, cwd, env, 120, 36)
	if err != nil {
		t.Fatalf("spawn %s: %v", exe, err)
	}
	defer func() { _ = created.Close() }()
	defer func() { _ = instance.Close() }()

	var output lockedTerminalOutput
	outputDone, err := windowssandbox.ReadHandleLoop(instance.TakeOutputRead(), func(chunk []byte) {
		output.Write(chunk)
	})
	if err != nil {
		t.Fatalf("read ConPTY output: %v", err)
	}
	inputWrite := windows.Handle(instance.TakeInputWrite())
	for _, command := range commands {
		var written uint32
		if err := windows.WriteFile(inputWrite, []byte(command+"\r"), &written, nil); err != nil {
			t.Fatalf("write %q to %s: %v", command, exe, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	_ = windows.CloseHandle(inputWrite)

	timeoutMS := int64(8000)
	outcome, err := windowssandbox.WaitCreatedProcess(created, &timeoutMS, windowssandbox.CancellationToken{})
	if err != nil {
		t.Fatalf("wait %s: %v output=%q", exe, err, output.String())
	}
	if outcome != windowssandbox.ProcessWaitExited {
		_ = windowssandbox.TerminateCreatedProcess(created, 1)
		t.Fatalf("%s did not exit: %s output=%q", exe, outcome, output.String())
	}
	exitCode, err := windowssandbox.CreatedProcessExitCode(created)
	if err != nil {
		t.Fatalf("exit code %s: %v", exe, err)
	}
	skipIfWindowsConPTYHostDLLInitFailed(t, exitCode, output.String())
	if exitCode != 0 {
		t.Fatalf("%s exited %d output=%q", exe, exitCode, output.String())
	}
	_ = instance.Close()
	select {
	case <-outputDone:
	case <-time.After(time.Second):
	}
	return output.String()
}

func spawnCurrentUserConPTYWithEnv(command []string, cwd string, env map[string]string, columns int16, rows int16) (*windowssandbox.CreatedProcess, *conpty.Instance, error) {
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
	envStrings := os.Environ()
	for key, value := range env {
		envStrings = append(envStrings, key+"="+value)
	}
	envBlock, err := environmentBlockUTF16Ptr(envStrings)
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
	creationFlags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	err = windows.CreateProcess(
		applicationName,
		&commandLine[0],
		nil,
		nil,
		false,
		creationFlags,
		envBlock,
		cwdPtr,
		&startupInfo.StartupInfo,
		&processInfo,
	)
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

func environmentBlockUTF16Ptr(env []string) (*uint16, error) {
	block := strings.Join(env, "\x00") + "\x00\x00"
	encoded := utf16.Encode([]rune(block))
	return &encoded[0], nil
}
