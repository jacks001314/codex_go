//go:build !windows

package execserver

import (
	"io"
	"os/exec"
	"testing"
	"time"
)

// Mirrors Rust's direct PTY terminal-query test: a subprocess that blocks on a
// cursor-position report receives the bounded response and exits.
func TestTTYRespondsToCursorPositionQueryLikeRust(t *testing.T) {
	script := "stty -echo -icanon; printf 'alpha\\033[6n'; dd bs=1 count=6 2>/dev/null; printf '\\nok'"
	started, supported, err := startExecServerTTY(exec.Command("/bin/sh", "-c", script))
	if err != nil || !supported {
		t.Skipf("PTY unavailable: supported=%v err=%v", supported, err)
	}
	reader := newTerminalQueryReader(started.reader, started.stdin)
	defer func() {
		_ = reader.Close()
		if started.cleanup != nil {
			_ = started.cleanup()
		}
	}()
	output := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(reader)
		output <- data
	}()
	wait := make(chan int, 1)
	go func() {
		code, _ := started.wait()
		wait <- code
	}()
	select {
	case code := <-wait:
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
	case <-time.After(5 * time.Second):
		if started.kill != nil {
			_ = started.kill()
		}
		t.Fatal("PTY subprocess did not exit after the cursor-position response")
	}
	select {
	case data := <-output:
		if string(data) != "alpha\x1b[1;1R\r\nok" {
			t.Fatalf("output = %q, want the echoed cursor report and final marker", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PTY output was not drained")
	}
}
