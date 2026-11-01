//go:build !windows

package execserver

import (
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"codex_go/shell"
)

// Rust #48078 keeps the captured state in executor memory and writes it to an
// unnamed file per launch: the child receives an independent reader and the
// pathname is gone before any state is written.
func TestShellSnapshotTransportIsUnnamedLikeRust(t *testing.T) {
	state := "export RESTORED=value\n"
	reader, err := materializeShellSnapshotTransport(shell.ShellBash, state)
	if err != nil {
		t.Fatalf("materializeShellSnapshotTransport() error = %v", err)
	}
	defer reader.Close()
	info, err := reader.Stat()
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Nlink != 0 {
		t.Fatalf("transport link count = %#v, want an unlinked file", info.Sys())
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	want := "\\exec 3<&-\n" + state
	if string(content) != want {
		t.Fatalf("transport content = %q, want %q", content, want)
	}
}

// The replay file closes the inherited carrier before the rest of the state is
// sourced, and the shell keeps its own reader for that file.
func TestShellSnapshotTransportReplaysThroughDescriptorLikeRust(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash unavailable: %v", err)
	}
	reader, err := materializeShellSnapshotTransport(shell.ShellBash, "RESTORED=value\n")
	if err != nil {
		t.Fatalf("materializeShellSnapshotTransport() error = %v", err)
	}
	defer reader.Close()
	command := exec.Command(bash, "-c", ". /dev/fd/3\nprintf '%s' \"${RESTORED:-missing}\"")
	command.ExtraFiles = []*os.File{reader}
	output, err := command.Output()
	if err != nil {
		t.Fatalf("bash source error = %v", err)
	}
	if string(output) != "value" {
		t.Fatalf("sourced output = %q, want the replayed state", output)
	}
}

// sh cannot portably close a descriptor above 9, so it keeps environment replay
// and never gets a transport file (Rust returns "unsupported snapshot shell").
func TestShellSnapshotTransportRejectsEnvironmentOnlyShellsLikeRust(t *testing.T) {
	for _, shellType := range []shell.ShellType{shell.ShellSh} {
		if _, err := materializeShellSnapshotTransport(shellType, "RESTORED=value\n"); err == nil {
			t.Fatalf("materializeShellSnapshotTransport(%s) error = nil", shellType)
		}
		if _, ok := shellSnapshotCarrierClose(shellType); ok {
			t.Fatalf("carrier close admitted %s", shellType)
		}
	}
}
