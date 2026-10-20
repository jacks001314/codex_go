//go:build unix

package rollout

import (
	"os"
	"syscall"
)

// preserveRolloutAccessMetadata copies ownership, permissions, mtime, and a
// platform ACL onto a replacement rollout before publication (Rust #55252).
// Chown can clear mode bits, so permissions are restored afterwards. ACLs are
// applied last because they may prohibit subsequent metadata updates.
func preserveRolloutAccessMetadata(sourcePath string, destinationPath string, sourceInfo os.FileInfo) error {
	if sourceInfo == nil {
		var err error
		sourceInfo, err = os.Stat(sourcePath)
		if err != nil {
			return err
		}
	}
	if stat, ok := sourceInfo.Sys().(*syscall.Stat_t); ok {
		destinationInfo, err := os.Stat(destinationPath)
		if err != nil {
			return err
		}
		destinationStat, _ := destinationInfo.Sys().(*syscall.Stat_t)
		if destinationStat == nil || destinationStat.Uid != stat.Uid || destinationStat.Gid != stat.Gid {
			if err := os.Chown(destinationPath, int(stat.Uid), int(stat.Gid)); err != nil {
				return err
			}
		}
	}
	if err := os.Chmod(destinationPath, sourceInfo.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(destinationPath, sourceInfo.ModTime(), sourceInfo.ModTime()); err != nil {
		return err
	}
	return copyPlatformRolloutACL(sourcePath, destinationPath)
}
