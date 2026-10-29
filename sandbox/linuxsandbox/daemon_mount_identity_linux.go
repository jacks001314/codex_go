//go:build linux

package linuxsandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// daemonSocketIdentity reports the opened socket directory's mount device as
// "major:minor" and its mount ID, mirroring Rust's
// `daemon_socket_mask_paths` (#46125): the ID comes from the descriptor's
// fdinfo entry, with a statx fallback for kernels that omit it there.
func daemonSocketIdentity(directory string) (string, string, error) {
	file, err := os.Open(directory)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", "", err
	}
	system, ok := info.Sys().(*unix.Stat_t)
	if !ok {
		return "", "", errDaemonSocketIsolation
	}
	device := fmt.Sprintf("%d:%d", unix.Major(uint64(system.Dev)), unix.Minor(uint64(system.Dev)))
	return device, daemonSocketMountID(file), nil
}

// daemonSocketMountID reads the mount ID shared with /proc/self/mountinfo. An
// empty result means the kernel reported none, which keeps the conservative
// no-mount-ID checks.
func daemonSocketMountID(file *os.File) string {
	if file == nil {
		return ""
	}
	if data, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", file.Fd())); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			rest, ok := strings.CutPrefix(line, "mnt_id:")
			if !ok {
				continue
			}
			id, err := strconv.ParseUint(strings.TrimSpace(rest), 10, 64)
			if err != nil {
				continue
			}
			return strconv.FormatUint(id, 10)
		}
	}
	var stat unix.Statx_t
	if err := unix.Statx(int(file.Fd()), "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID, &stat); err == nil &&
		stat.Mask&unix.STATX_MNT_ID != 0 {
		return strconv.FormatUint(stat.Mnt_id, 10)
	}
	return ""
}
