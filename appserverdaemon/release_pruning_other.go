//go:build !windows && !linux

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// ownedMount mirrors Rust release_pruning::owned_mount (release_pruning.rs:93-132):
// only ordinary files and directories can belong to a release, and non-Linux Unix
// identifies the mount by its device id.
func ownedMount(path string) (string, error) {
	meta, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !meta.IsDir() && !meta.Mode().IsRegular() {
		return "", errors.New("not an ordinary package entry")
	}
	stat, ok := meta.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("unknown package metadata")
	}
	return strconv.FormatUint(uint64(stat.Dev), 10), nil
}

// selectionIdentity mirrors Rust's Unix branch (release_pruning.rs:24-35) with the
// portable timestamps this platform exposes: device + inode + modification time.
// Rust uses the inode change time, which identifies a recreated link just as well
// because both are re-read from the live link.
func selectionIdentity(path string) (string, error) {
	meta, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	stat, ok := meta.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("unknown selection metadata")
	}
	modTime := meta.ModTime()
	return fmt.Sprintf("%d %d %d.%09d", stat.Dev, stat.Ino, modTime.Unix(), modTime.Nanosecond()), nil
}
