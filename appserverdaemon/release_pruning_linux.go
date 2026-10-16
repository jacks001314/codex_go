//go:build linux

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// ownedMount mirrors Rust release_pruning::owned_mount (release_pruning.rs:93-132)
// on Linux: only ordinary files and directories can belong to a release, and the
// mount id comes from procfs because device ids alone cannot distinguish
// same-device bind mounts. A missing procfs disables pruning.
func ownedMount(path string) (string, error) {
	meta, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !meta.IsDir() && !meta.Mode().IsRegular() {
		return "", errors.New("not an ordinary package entry")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer syscall.Close(fd)
	info, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", fd))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(info), "\n") {
		if mountID, found := strings.CutPrefix(line, "mnt_id:"); found {
			return strings.TrimSpace(mountID), nil
		}
	}
	return "", errors.New("unknown package mount")
}

// selectionIdentity mirrors Rust's Unix branch (release_pruning.rs:24-35):
// device, inode and change time identify the `current` link that was recorded.
func selectionIdentity(path string) (string, error) {
	meta, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	stat, ok := meta.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("unknown selection metadata")
	}
	return fmt.Sprintf("%d %d %d.%09d", stat.Dev, stat.Ino, stat.Ctim.Sec, stat.Ctim.Nsec), nil
}
