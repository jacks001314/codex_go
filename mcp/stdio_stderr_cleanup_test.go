//go:build !windows

package mcp

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust stdio_stderr_cleanup (#43870): an escaped descendant can keep the
// stderr pipe open after the server exits. Teardown must drain queued
// diagnostics briefly and then close the pipe instead of waiting for EOF, so the
// shutdown succeeds and descriptors are released.
func TestWaitMCPStdioCommandDrainsStderrWhenDescendantKeepsPipeOpen(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sh -c 'sleep 5' & echo queued-diagnostic; exit 0")
	buffer := &stdioOutputBuffer{}
	cmd.Stderr = buffer
	cmd.WaitDelay = mcpStdioStderrDrainGrace
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	started := time.Now()
	err := waitMCPStdioCommand(cmd)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("waitMCPStdioCommand() error = %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("teardown waited %s; expected the drain grace to bound it", elapsed)
	}
	if !strings.Contains(buffer.String(), "queued-diagnostic") {
		t.Fatalf("queued diagnostics were not drained: %q", buffer.String())
	}
}
