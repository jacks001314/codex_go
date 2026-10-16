//go:build linux

package state

import (
	"golang.org/x/sys/unix"
)

// nfsSuperMagic is the Linux superblock magic of an NFS mount; Rust compares
// `rustix::fs::statfs(path)?.f_type` against `rustix::fs::NFS_SUPER_MAGIC`
// (codex-rs/file-system/src/nfs.rs, Rust #54417 `9c84e89c`).
const nfsSuperMagic = 0x6969

// isNFSPath reports whether path sits on an NFS mount on Linux.
func isNFSPath(path string) (bool, error) {
	var statfs unix.Statfs_t
	if err := unix.Statfs(path, &statfs); err != nil {
		return false, err
	}
	return statfs.Type == nfsSuperMagic, nil
}
