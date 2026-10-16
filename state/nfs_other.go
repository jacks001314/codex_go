//go:build !linux

package state

// isNFSPath reports false on every other platform: Rust only detects NFS on
// Linux and leaves the existing filesystem behavior in place elsewhere
// (codex-rs/file-system/src/nfs.rs, Rust #54417 `9c84e89c`).
func isNFSPath(path string) (bool, error) {
	return false, nil
}
