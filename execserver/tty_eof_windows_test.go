//go:build windows

package execserver

import (
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Mirrors Rust #45504: the exec-server ConPTY session drops the pseudoconsole's
// creation handles after a successful spawn, so its output reader reaches EOF
// once the last attached client exits instead of blocking while the session
// lives (which previously forced callers to close the PTY to stop the reader).
func TestExecServerTTYOutputReachesEOFWithoutClosingThePTY(t *testing.T) {
	command := os.Getenv("ComSpec")
	if strings.TrimSpace(command) == "" {
		command = "cmd.exe"
	}
	started, supported, err := startExecServerTTY(exec.Command(command, "/d", "/c", "exit 0"))
	if err != nil {
		t.Skipf("host ConPTY probe failed: %v", err)
	}
	if !supported {
		t.Skip("host does not support ConPTY")
	}
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, started.reader)
		close(readDone)
	}()
	_, waitErr := started.wait()

	blocked := false
	select {
	case <-readDone:
	case <-time.After(10 * time.Second):
		blocked = true
		_ = started.reader.Close()
		<-readDone
	}
	if started.closePTY != nil {
		_ = started.closePTY()
	}
	if started.cleanup != nil {
		_ = started.cleanup()
	}
	if waitErr != nil {
		t.Skipf("host ConPTY probe wait failed: %v", waitErr)
	}
	if blocked {
		t.Fatal("the exec-server TTY reader did not observe EOF after the child exited")
	}
}
