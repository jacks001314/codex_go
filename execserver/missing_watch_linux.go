//go:build linux

package execserver

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// Local Linux filesystems where an inotify watch observes changes made by this
// host. A network mount can accept a watch yet never report another host's
// changes, so those observations are never reused (Rust #57598).
const (
	missingWatchExtFilesystemMagic     = 0xEF53
	missingWatchXFSFilesystemMagic     = 0x58465342
	missingWatchBtrfsFilesystemMagic   = 0x9123683E
	missingWatchTmpFilesystemMagic     = 0x01021994
	missingWatchOverlayFilesystemMagic = 0x794C7630
)

func missingWatchFileSystemSupported(path string) bool {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return false
	}
	switch int64(stat.Type) {
	case missingWatchExtFilesystemMagic,
		missingWatchXFSFilesystemMagic,
		missingWatchBtrfsFilesystemMagic,
		missingWatchTmpFilesystemMagic,
		missingWatchOverlayFilesystemMagic:
		return true
	default:
		return false
	}
}

// missingWatchMountSource arms mount-table notifications before directory
// watches are installed. inotify stays attached to a covered directory, so a
// bind mount or unmount must invalidate through this separate source.
func missingWatchMountSource() (<-chan struct{}, func()) {
	changed := make(chan struct{}, 1)
	stop := make(chan struct{})
	file, err := os.Open("/proc/self/mounts")
	if err != nil {
		close(stop)
		return nil, func() {}
	}
	fd := int(file.Fd())
	go func() {
		defer file.Close()
		for {
			pollFDs := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLPRI}}
			count, err := unix.Poll(pollFDs, 500)
			if err != nil {
				if errors.Is(err, unix.EINTR) {
					continue
				}
				return
			}
			if count > 0 {
				select {
				case changed <- struct{}{}:
				default:
				}
				return
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	var once sync.Once
	return changed, func() { once.Do(func() { close(stop) }) }
}
