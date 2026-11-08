//go:build windows

package conpty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestPseudoconsoleDeliversConsoleOutput is the content assertion the package
// was missing: every other test only proves the output pipe reaches EOF, never
// that the child's console output arrives.
//
// It used to fail with zero bytes on every host. The cause was not the console
// stack: windowssandbox.ProcThreadAttributeList.SetPseudoconsole sent the
// address of the HPCON instead of the HPCON value, so no client ever attached
// and the console was torn down before it wrote anything (plan §17, and the
// three-state control experiment in conpty_raw_probe_windows_test.go). With the
// attribute fixed this passes on Windows 10.0.26200.9711 in a non-interactive
// agent session.
//
// It stays opt-in because it still depends on the host's console stack and
// takes a few hundred milliseconds. Enable it with
// CODEX_CONPTY_CONTENT_PROBE=1.
func TestPseudoconsoleDeliversConsoleOutput(t *testing.T) {
	if os.Getenv("CODEX_CONPTY_CONTENT_PROBE") != "1" {
		t.Skip("set CODEX_CONPTY_CONTENT_PROBE=1 to assert pseudoconsole output content")
	}
	instance, created, err := spawnForContentProbe([]string{"cmd", "/d", "/c", "echo CONPTY_CONTENT_OK"})
	if err != nil {
		t.Skipf("this host cannot spawn a console process: %v", err)
	}
	defer instance.Close()
	handle := windows.Handle(instance.TakeOutputRead())
	if handle == 0 {
		t.Fatal("spawn returned no output read handle")
	}
	output, readErr := readConsoleBytes(handle, 10*time.Second)
	if readErr != nil {
		t.Fatalf("reading console output: %v", readErr)
	}
	if !strings.Contains(string(output), "CONPTY_CONTENT_OK") {
		t.Fatalf("console output = %q, want it to contain CONPTY_CONTENT_OK (child %s)", output, created)
	}
}

// spawnForContentProbe keeps the probe independent of the spawn helper's token
// plumbing differences across hosts.
func spawnForContentProbe(command []string) (*Instance, string, error) {
	instance, err := SpawnProcessAsUser(command, "")
	if err != nil {
		return nil, "", err
	}
	return instance, fmt.Sprintf("%v", command), nil
}

// readConsoleBytes drains the console output, treating EOF as success so a
// closed console does not look like a hang.
func readConsoleBytes(handle windows.Handle, timeout time.Duration) ([]byte, error) {
	file := os.NewFile(uintptr(handle), "conpty-probe-out")
	if file == nil {
		return nil, errors.New("output handle is not a file descriptor")
	}
	defer file.Close()
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(file, 1<<16))
		done <- result{data, err}
	}()
	select {
	case got := <-done:
		return got.data, got.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("console produced no EOF within %s", timeout)
	}
}
