//go:build windows

package execserver

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"codex_go/sandbox"
	"codex_go/sandbox/mxc"
)

// TestExecServerMXCTTYDeliversConsoleContent is the end-to-end proof for
// windows-mxc-tty-launch: a TTY request on the mxc level runs the workload
// through the two-stage launcher (exec-server executable as the stage one
// helper, PSEC for the workload) and the workload's console output reaches the
// session readers. Asserting content rather than "a process started" is the
// point: before plan section 17 a TTY launch could start a process that wrote
// nothing to the pseudoconsole.
func TestExecServerMXCTTYDeliversConsoleContent(t *testing.T) {
	if !sandbox.WindowsMXCAvailable() {
		t.Skip("native MXC is unavailable on this executor")
	}
	if !mxc.NativeTTYLaunchSupported() {
		t.Fatalf("native MXC TTY support is off: %s", mxc.TTYUnsupportedMessage)
	}
	const marker = "EXECSERVER_MXC_TTY_OK"
	cwd := t.TempDir()
	payload, err := json.Marshal(FileSystemSandboxContext{
		Permissions:         json.RawMessage(`{"type":"managed","file_system":{"type":"restricted","entries":[]},"network":"restricted"}`),
		WorkspaceRoots:      []string{cwd},
		WindowsSandboxLevel: windowsSandboxLevelMxc,
	})
	if err != nil {
		t.Fatalf("Marshal(sandbox context) error = %v", err)
	}
	shell := strings.TrimSpace(os.Getenv("COMSPEC"))
	if shell == "" {
		shell = "cmd.exe"
	}
	process, supported, err := startExecServerSandboxProcess(&ExecParams{
		ProcessID: "mxc-tty-1",
		Argv:      []string{shell, "/d", "/c", "echo " + marker},
		CWD:       cwd,
		Env:       map[string]string{},
		TTY:       true,
		Sandbox:   payload,
	})
	if !supported {
		t.Fatal("tty launch: supported = false, want true")
	}
	if err != nil {
		t.Fatalf("tty launch err = %v", err)
	}
	if process == nil {
		t.Fatal("tty launch returned no process")
	}
	defer func() { _ = process.close() }()
	output := make(chan string, 1)
	go func() {
		var builder strings.Builder
		for _, reader := range process.readers {
			if reader == nil {
				continue
			}
			data, _ := io.ReadAll(reader)
			builder.Write(data)
		}
		output <- builder.String()
	}()
	code, err := process.wait()
	if err != nil {
		t.Fatalf("tty wait err = %v", err)
	}
	if code != 0 {
		t.Fatalf("tty exit code = %d, want 0", code)
	}
	select {
	case text := <-output:
		if !strings.Contains(text, marker) {
			t.Fatalf("console output = %q, want it to contain %q", text, marker)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out reading the MXC TTY console")
	}
}
