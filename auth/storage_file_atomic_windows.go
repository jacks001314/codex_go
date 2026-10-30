//go:build windows

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// createAuthFileReplacement creates the temporary credential file with the
// destination's DACL already applied and protected from parent inheritance, so
// no secret ever sits in a file with broader access (Rust #58251
// storage_file_windows).
func createAuthFileReplacement(path string) (*os.File, error) {
	descriptor, err := destinationSecurityInfo(path)
	if err != nil {
		return nil, err
	}
	var dacl *windows.ACL
	if descriptor != nil {
		dacl, _, err = descriptor.DACL()
		if err != nil {
			return nil, err
		}
	}
	dir := filepath.Dir(path)
	for range 32 {
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			return nil, err
		}
		name := filepath.Join(dir, ".auth-"+hex.EncodeToString(suffix))
		// CREATE_NEW prevents opening an attacker-created temporary path.
		handle, err := windows.CreateFile(
			windows.StringToUTF16Ptr(name),
			windows.GENERIC_READ|windows.GENERIC_WRITE|windows.WRITE_DAC,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil,
			windows.CREATE_NEW,
			windows.FILE_ATTRIBUTE_NORMAL,
			0,
		)
		if err != nil {
			if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
				continue
			}
			return nil, err
		}
		if dacl != nil {
			// Protect the captured DACL from parent inheritance before any
			// secret is written (Rust #58251 storage_file_windows).
			if err := windows.SetSecurityInfo(
				handle,
				windows.SE_FILE_OBJECT,
				windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
				nil, nil, dacl, nil,
			); err != nil {
				_ = windows.CloseHandle(handle)
				_ = os.Remove(name)
				return nil, err
			}
		}
		return os.NewFile(uintptr(handle), name), nil
	}
	return nil, errors.New("failed to create a credential replacement file")
}

// destinationSecurityInfo returns the current credential file's DACL. A
// missing destination has no DACL to preserve.
func destinationSecurityInfo(path string) (*windows.SECURITY_DESCRIPTOR, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	return windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
}

// checkAuthFileLinks rejects hard-linked credential files: replacing one name
// would silently split the identity it shares with its peer (Rust #58251).
func checkAuthFileLinks(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return err
	}
	if info.NumberOfLinks > 1 {
		return &os.PathError{Op: "replace", Path: path, Err: fmt.Errorf("atomic credential replacement does not support hard-linked files: %w", errors.ErrUnsupported)}
	}
	return nil
}
