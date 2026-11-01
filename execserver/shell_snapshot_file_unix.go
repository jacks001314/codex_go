//go:build !windows

package execserver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"codex_go/codexuds"
	"codex_go/shell"
)

// materializeShellSnapshotTransport writes one launch's captured state into an
// unnamed file and returns an independent reader for the child (Rust #48078).
//
// The file is created under the daemon socket root that every
// filesystem-restricted sandbox hides, opened twice (writing must not advance
// the reader), and unlinked before the state is written. The cached snapshot
// therefore never exists as a named file that a sandboxed command could read,
// replace, or keep open.
func materializeShellSnapshotTransport(shellType shell.ShellType, state string) (*os.File, error) {
	carrierClose, ok := shellSnapshotCarrierClose(shellType)
	if !ok {
		return nil, fmt.Errorf("shell snapshots cannot be sourced by %s", shellType)
	}
	root, err := codexuds.PrepareSharedDaemonSocketDirectory()
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(root, "snapshot-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "state")
	writer, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		_ = os.Remove(directory)
		return nil, err
	}
	reader, err := os.Open(path)
	if err != nil {
		_ = writer.Close()
		_ = os.RemoveAll(directory)
		return nil, err
	}
	cleanup := func() {
		_ = reader.Close()
		_ = writer.Close()
		_ = os.RemoveAll(directory)
	}
	if err := os.Remove(path); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.Remove(directory); err != nil {
		cleanup()
		return nil, err
	}
	info, err := writer.Stat()
	if err != nil {
		cleanup()
		return nil, err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink != 0 {
		cleanup()
		return nil, errors.New("shell snapshot transport is still linked")
	}
	// Source opens its own reader before this runs, so the carrier descriptor can
	// be closed before the functions that should be restored are defined.
	if _, err := writer.WriteString(carrierClose + state); err != nil {
		cleanup()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		_ = reader.Close()
		return nil, err
	}
	return reader, nil
}

// shellSnapshotCarrierClose returns the first line of the replay file, which
// drops the inherited carrier descriptor without shadowing exec/unset later.
func shellSnapshotCarrierClose(shellType shell.ShellType) (string, bool) {
	switch shellType {
	case shell.ShellBash, shell.ShellZsh:
		return fmt.Sprintf("\\exec %d<&-\n", shellSnapshotCarrierFd), true
	default:
		return "", false
	}
}
