package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Mirrors Rust #48238: a local stdio MCP server must not create a console
// window, for a direct program and for a batch shim launched through cmd.exe.
func TestMCPStdioCommandSuppressesConsoleWindow(t *testing.T) {
	for _, command := range []string{`C:\Program Files\nodejs\node.exe`, `C:\Program Files\nodejs\npx.cmd`} {
		cmd := newMCPStdioCommand(command, "-y", "@gebrai/gebrai")
		if cmd.SysProcAttr == nil {
			t.Fatalf("%s: SysProcAttr is nil", command)
		}
		if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
			t.Fatalf("%s: creation flags = %#x, want CREATE_NO_WINDOW", command, cmd.SysProcAttr.CreationFlags)
		}
	}
}

// The job-contained launch suspends the child before assignment; the console
// suppression must survive that replacement (Rust #48238).
func TestMCPStdioProcessKeepsSuspensionAndConsoleSuppression(t *testing.T) {
	cmd := newMCPStdioCommand(`C:\Program Files\nodejs\node.exe`, "--version")
	suspendMCPStdioForJobAssignment(cmd)
	flags := cmd.SysProcAttr.CreationFlags
	if flags&windows.CREATE_SUSPENDED == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_SUSPENDED", flags)
	}
	if flags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x, want CREATE_NO_WINDOW", flags)
	}
}

// Mirrors Rust #48238's Windows regression test: a direct launch and a launch
// through cmd.exe both produce a server with no console window, which then
// initializes and exits after shutdown.
func TestMCPStdioLaunchedServerHasNoConsoleWindow(t *testing.T) {
	executable, args := helperMCPServerCommand(t)
	dir := t.TempDir()
	shim := filepath.Join(dir, "mcp-helper.cmd")
	shimBody := "@echo off\r\n\"" + executable + "\" " + strings.Join(args, " ") + " %*\r\n"
	if err := os.WriteFile(shim, []byte(shimBody), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	for _, launch := range []struct {
		name    string
		command string
		args    []string
	}{
		{name: "direct", command: executable, args: args},
		{name: "cmd shim", command: shim},
	} {
		t.Run(launch.name, func(t *testing.T) {
			stateFile := filepath.Join(t.TempDir(), "console-state.txt")
			cmd := newMCPStdioCommand(launch.command, launch.args...)
			cmd.Env = append(os.Environ(),
				"GO_WANT_MCP_HELPER=1",
				"MCP_TEST_CONSOLE_STATE_FILE="+stateFile,
			)
			cmd.Stdin = strings.NewReader("")
			process, err := startMCPStdioProcess(cmd)
			if err != nil {
				t.Fatalf("startMCPStdioProcess() error = %v", err)
			}
			defer func() {
				process.terminate(cmd)
				_ = cmd.Wait()
				process.release()
			}()
			deadline := time.Now().Add(30 * time.Second)
			for {
				data, readErr := os.ReadFile(stateFile)
				if readErr == nil {
					if state := strings.TrimSpace(string(data)); state != "false" {
						t.Fatalf("console state = %q, want no console window", state)
					}
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("the server did not report its console state: %v", readErr)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestNewMCPStdioCommandRunsBatchShimWithArguments(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "command with spaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	shim := filepath.Join(dir, "mcp-shim.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\necho %~1^|%~2\r\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	output, err := newMCPStdioCommand(shim, "-y", "@gebrai/gebrai").CombinedOutput()
	if err != nil {
		t.Fatalf("batch shim failed: %v\n%s", err, output)
	}
	if got, want := strings.TrimSpace(string(output)), "-y|@gebrai/gebrai"; got != want {
		t.Fatalf("batch shim output = %q, want %q", got, want)
	}
}

func TestWindowsBatchCommandLineQuotesPathAndArguments(t *testing.T) {
	got := windowsBatchCommandLine(`C:\Program Files\nodejs\npx.cmd`, []string{"-y", "@gebrai/gebrai", "two words"})
	want := `/d /s /c ""C:\Program Files\nodejs\npx.cmd" "-y" "@gebrai/gebrai" "two words""`
	if got != want {
		t.Fatalf("windowsBatchCommandLine() = %q, want %q", got, want)
	}
}
