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

func requireExecServerTTYOutput(t *testing.T) {
	t.Helper()
	command := os.Getenv("ComSpec")
	if strings.TrimSpace(command) == "" {
		command = "cmd.exe"
	}
	started, supported, err := startExecServerTTY(exec.Command(command, "/d", "/c", "echo CODEX_EXEC_SERVER_PTY_PROBE"))
	if err != nil {
		t.Skipf("host ConPTY probe failed: %v", err)
	}
	if !supported {
		t.Skip("host does not support ConPTY")
	}
	readDone := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(started.reader)
		readDone <- data
	}()
	_, waitErr := started.wait()
	if started.closePTY != nil {
		_ = started.closePTY()
	}
	var output []byte
	select {
	case output = <-readDone:
	case <-time.After(time.Second):
		_ = started.reader.Close()
		output = <-readDone
	}
	if started.cleanup != nil {
		_ = started.cleanup()
	}
	if waitErr != nil {
		t.Skipf("host ConPTY probe wait failed: %v", waitErr)
	}
	if !strings.Contains(string(output), "CODEX_EXEC_SERVER_PTY_PROBE") {
		t.Skipf("host ConPTY output is unavailable; output=%q", output)
	}
}
