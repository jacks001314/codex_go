//go:build windows

package appserverdaemon

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// fileReadAttributes is FILE_READ_ATTRIBUTES, which stdlib syscall does not export.
const fileReadAttributes = 0x00000080

// ownedMount mirrors Rust release_pruning::owned_mount's Windows branch
// (release_pruning.rs:101-110): only ordinary files and directories can belong to
// a release, and a reparse point (a junction, a symlink or a volume mount point)
// makes the release unrecognized. Volume mount points are reparse points, so the
// Windows mount id is always "0", exactly like Rust.
func ownedMount(path string) (string, error) {
	meta, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !meta.IsDir() && !meta.Mode().IsRegular() {
		return "", errors.New("not an ordinary package entry")
	}
	if attributes, ok := meta.Sys().(*syscall.Win32FileAttributeData); ok &&
		attributes.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return "", errors.New("package entry is a reparse point")
	}
	return "0", nil
}

// selectionIdentity mirrors Rust's Windows branch (release_pruning.rs:37-74): open
// the link itself (FILE_FLAG_OPEN_REPARSE_POINT) and identify it by the volume and
// file id of the reparse point. Rust additionally mixes in the USN journal record
// to notice a replaced file that reuses an id; Go has no USN access, so a replaced
// link with a recycled file id would be treated as current. The history is written
// and verified within one install under the installer lock, so the window is the
// install itself, the same guarantee Rust's record gives for a plain recreate.
func selectionIdentity(path string) (string, error) {
	handle, err := syscall.CreateFile(
		syscall.StringToUTF16Ptr(path),
		fileReadAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(handle)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x-%x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
