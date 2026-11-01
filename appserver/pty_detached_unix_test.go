//go:build !windows

package appserver

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rust bf3eb2ec91 (#40460) drives Unix PTY reads through nonblocking readiness
// so a detached child that still holds the terminal can never keep shutdown
// waiting. Go parked the reader in a blocking read on the master, so the drain
// that follows the leader's exit never finished until that child died.
func TestCommandExecTTYReturnsWhileDetachedChildHoldsTerminalLikeRust(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available for the detached-child fixture")
	}
	cwd := t.TempDir()
	script := filepath.Join(cwd, "detached.py")
	// The detached child keeps the slave descriptors but outlives the original
	// process group, like Rust's fixture: it forks, leaves the session, ignores
	// SIGHUP and waits on its own terminal input.
	const fixture = `import os, select, signal, time, tty
if os.fork() == 0:
    os.setsid()
    signal.signal(signal.SIGHUP, signal.SIG_IGN)
    tty.setraw(0)
    print("__detached_pid:" + str(os.getpid()), flush=True)
    select.select([0], [], [])
    time.sleep(30)
    os._exit(0)
time.sleep(0.3)
os._exit(0)
`
	if writeErr := os.WriteFile(script, []byte(fixture), 0o600); writeErr != nil {
		t.Fatalf("write fixture = %v", writeErr)
	}
	t.Cleanup(func() {
		// The fixture outlives the session on purpose, so it is reaped by name.
		_ = exec.Command("pkill", "-f", script).Run()
	})

	processID := "tty-detached-child"
	params := &CommandExecParams{
		Command:   []string{python, script},
		ProcessID: &processID,
		TTY:       true,
		Size:      &TerminalSize{Rows: 24, Cols: 80},
	}
	service := NewCommandExecService()
	service.DefaultTimeoutMS = 60_000
	sink := NewNotificationBuffer()

	started := time.Now()
	responseCh := make(chan *CommandExecResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		response, execErr := service.ExecuteWithNotify(context.Background(), params, cwd, sinkNotifyFunc(sink))
		if execErr != nil {
			errCh <- execErr
			return
		}
		responseCh <- response
	}()

	select {
	case err := <-errCh:
		t.Fatalf("command/exec error = %v", err)
	case response := <-responseCh:
		if response.ExitCode != 0 {
			t.Fatalf("command/exec response = %+v, want exit code 0", response)
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			t.Fatalf("command/exec waited %s for a detached child that still holds the terminal", elapsed)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("command/exec never returned: the PTY reader stayed parked on the detached child")
	}
	if !strings.Contains(detachedOutput(sink, processID), "__detached_pid:") {
		t.Fatalf("output deltas = %q, want the detached child's announcement", detachedOutput(sink, processID))
	}
}

func detachedOutput(sink *NotificationBuffer, processID string) string {
	var output strings.Builder
	for _, notification := range sink.List() {
		delta, ok := notification.Params.(*CommandExecOutputDeltaNotification)
		if !ok || delta.ProcessID != processID || delta.Stream != StreamStdout {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
		if err != nil {
			continue
		}
		output.Write(decoded)
	}
	return output.String()
}

// Rust bf3eb2ec91 (#40460) ends portable-PTY stdin with portable-pty's newline
// plus the terminal's VEOF byte. Go wrote a bare ^D, which drops the newline
// that completes a partial line, so a reader of the terminal saw "partial"
// instead of "partial\n".
func TestProcessTTYStdinCloseSendsNewlineAndVEOFLikeRust(t *testing.T) {
	cwd := t.TempDir()
	outPath := filepath.Join(cwd, "stdin.txt")
	service := NewProcessService()
	service.DefaultTimeoutMS = 10_000
	sink := NewNotificationBuffer()
	handle := "tty-stdin-close"
	params := &ProcessSpawnParams{
		Command:       []string{"sh", "-c", "cat > " + shellQuote(outPath)},
		ProcessHandle: handle,
		CWD:           cwd,
		TTY:           true,
		Size:          &TerminalSize{Rows: 24, Cols: 80},
	}
	if _, err := service.Spawn(context.Background(), params, sinkNotifyFunc(sink)); err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	partial := base64.StdEncoding.EncodeToString([]byte("partial"))
	if _, err := service.WriteStdin(&ProcessWriteStdinParams{ProcessHandle: handle, DeltaBase64: &partial}); err != nil {
		t.Fatalf("WriteStdin(partial) error = %v", err)
	}
	if _, err := service.WriteStdin(&ProcessWriteStdinParams{ProcessHandle: handle, CloseStdin: true}); err != nil {
		t.Fatalf("WriteStdin(close) error = %v", err)
	}
	exited := waitForProcessExited(t, sink, handle).Params.(*ProcessExitedNotification)
	if exited.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", exited.ExitCode)
	}
	data, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("read %s error = %v", outPath, readErr)
	}
	if string(data) != "partial\n" {
		t.Fatalf("terminal input = %q, want %q", string(data), "partial\n")
	}
}
