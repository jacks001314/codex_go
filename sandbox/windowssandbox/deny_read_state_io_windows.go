//go:build windows

package windowssandbox

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openDenyReadACLStateForRead opens the state leaf without following reparse
// points. A stable (recovery) read excludes live writers so a malformed file
// cannot be rebuilt while a legacy writer is mid-update, matching Rust #50940.
func openDenyReadACLStateForRead(path string, stable bool) (*os.File, error) {
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	if stable {
		share = windows.FILE_SHARE_READ | windows.FILE_SHARE_DELETE
	}
	return openDenyReadACLStateLeaf(path, windows.GENERIC_READ, share, windows.OPEN_EXISTING)
}

// openDenyReadACLStateForWrite creates the state leaf without truncating it and
// without granting delete sharing, so the validated file cannot be replaced
// between validation and the in-place write.
func openDenyReadACLStateForWrite(path string) (*os.File, error) {
	return openDenyReadACLStateLeaf(
		path,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.OPEN_ALWAYS,
	)
}

func openDenyReadACLStateLeaf(path string, access uint32, share uint32, disposition uint32) (*os.File, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pointer,
		access,
		share,
		nil,
		disposition,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// validateDenyReadACLStateFile rejects a leaf that is a reparse point or has
// multiple links, so recovery never writes through a link to someone else's
// file (Rust #50940).
func validateDenyReadACLStateFile(file *os.File) error {
	if file == nil {
		return errors.New("state file is not open")
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("state file is a reparse point")
	}
	if info.NumberOfLinks != 1 {
		return errors.New("state file has multiple links")
	}
	return nil
}

func isDenyReadStateSharingViolation(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
