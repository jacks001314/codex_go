package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	mcpProcessTreeHelperEnv = "GO_WANT_MCP_PROCESS_TREE_HELPER"
	mcpProcessTreeRoleEnv   = "MCP_PROCESS_TREE_ROLE"
	mcpProcessTreePIDEnv    = "MCP_PROCESS_TREE_PID_FILE"
)

// runMCPProcessTreeHelper is re-executed as the direct child in
// TestStdioProcessTerminationReachesTheWholeTree. It spawns a grandchild, records
// the grandchild PID for the test, and then blocks so the tree only disappears
// when teardown reaches it.
func runMCPProcessTreeHelper() {
	if os.Getenv(mcpProcessTreeRoleEnv) == "grandchild" {
		// Block forever; the test kills this process as part of the tree.
		for {
			time.Sleep(time.Hour)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	grandchild := exec.Command(exe)
	grandchild.Env = []string{
		mcpProcessTreeHelperEnv + "=1",
		mcpProcessTreeRoleEnv + "=grandchild",
	}
	if err := grandchild.Start(); err != nil {
		os.Exit(3)
	}
	pidFile := os.Getenv(mcpProcessTreePIDEnv)
	if pidFile == "" {
		os.Exit(4)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(grandchild.Process.Pid)), 0o600); err != nil {
		os.Exit(5)
	}
	for {
		time.Sleep(time.Hour)
	}
}

// Mirrors Rust's local stdio MCP server containment (#10710; the launcher moved
// onto the shared pty command in #46660, keeping `ProcessMode::NewGroup` on Unix
// and the suspended spawn + Job Object on Windows): terminating the transport
// must tear down descendants the server spawned, not just the direct child.
func TestStdioProcessTerminationReachesTheWholeTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{
		mcpProcessTreeHelperEnv + "=1",
		mcpProcessTreeRoleEnv + "=parent",
		mcpProcessTreePIDEnv + "=" + pidFile,
	}
	process, err := startMCPStdioProcess(cmd)
	if err != nil {
		t.Fatalf("startMCPStdioProcess() error = %v", err)
	}
	if !process.contained() {
		process.terminate(cmd)
		_ = waitMCPStdioCommand(cmd)
		process.release()
		t.Skip("process containment is unavailable on this host")
	}
	t.Cleanup(func() {
		process.terminate(cmd)
		_ = waitMCPStdioCommand(cmd)
		process.release()
	})

	grandchildPID := awaitMCPTestPID(t, pidFile)
	if !mcpTestProcessAlive(grandchildPID) {
		t.Fatalf("grandchild %d died before teardown", grandchildPID)
	}

	process.terminate(cmd)
	_ = waitMCPStdioCommand(cmd)
	process.release()

	if !awaitMCPTestProcessExit(grandchildPID, 10*time.Second) {
		t.Fatalf("grandchild %d survived teardown; the server tree was not contained", grandchildPID)
	}
}

func awaitMCPTestPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("helper never recorded a grandchild pid in %s", path)
	return 0
}

func awaitMCPTestProcessExit(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !mcpTestProcessAlive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !mcpTestProcessAlive(pid)
}
