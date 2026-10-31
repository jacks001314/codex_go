//go:build windows

package windowssandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	json "github.com/goccy/go-json"
	"golang.org/x/sys/windows"
)

// OpenProvisioningSetupLog opens one fresh log relative to a pinned sandbox
// directory instead of appending to a caller-controlled log entry (Rust
// setup_log_writer, #42342). The parent handle is retained so the entry stays
// bound to the validated directory for the life of the helper.
func OpenProvisioningSetupLog(baseDir string) (*os.File, error) {
	file, directory, err := CreateTemporaryFileInDir(baseDir, ".log")
	if err != nil {
		return nil, err
	}
	// The fresh log was opened without FILE_SHARE_DELETE, so its directory entry
	// cannot be renamed or replaced while the helper writes to it.
	windows.CloseHandle(directory)
	return file, nil
}

// PrepareProvisioningSetupMarker creates the setup marker relative to a pinned,
// reparse-free sandbox directory with its protected DACL installed at creation
// time, and retains the exclusive handle for the whole provisioning run
// (Rust #42342 PreparedSetupMarker::Retained).
func PrepareProvisioningSetupMarker(codexHome string, realUser string) (*os.File, error) {
	dir := SandboxDir(codexHome)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	markerPath := SetupMarkerPath(codexHome)
	if err := os.Remove(markerPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove setup marker file %s: %w", markerPath, err)
	}
	directory, err := OpenDirectoryNoReparse(
		dir,
		windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		DirectoryOpenExisting,
	)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(directory)
	nameUTF16, err := windows.UTF16FromString(filepath.Base(markerPath))
	if err != nil {
		return nil, err
	}
	securityDescriptor, err := markerSecurityDescriptor(realUser)
	if err != nil {
		return nil, fmt.Errorf("protect setup marker file %s: %w", markerPath, err)
	}
	handle, err := openNoReparseWithSecurityDescriptor(
		directory,
		nameUTF16,
		windows.FILE_READ_DATA|windows.FILE_WRITE_DATA|windows.READ_CONTROL|windows.DELETE|windows.SYNCHRONIZE,
		0,
		windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		securityDescriptor,
	)
	runtime.KeepAlive(securityDescriptor)
	if err != nil {
		return nil, fmt.Errorf("create protected setup marker file %s: %w", markerPath, err)
	}
	return os.NewFile(uintptr(handle), markerPath), nil
}

// WriteProvisioningSetupMarker writes the committed marker through the retained
// handle, so no caller can swap the marker entry after preparation.
func WriteProvisioningSetupMarker(file *os.File, marker *SetupMarker) error {
	if file == nil || marker == nil {
		return ErrInvalidRequest
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	return err
}

// markerSecurityDescriptor builds the protected marker DACL Rust installs at
// creation time: full access for SYSTEM, Administrators and the real user, with
// inheritance blocked (SDDL "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;<user>)",
// Rust #42342 prepare_setup_marker).
func markerSecurityDescriptor(realUser string) (*windows.SECURITY_DESCRIPTOR, error) {
	realUserSID, found, err := lookupLocalUserSID(realUser)
	if err != nil {
		return nil, fmt.Errorf("resolve real user SID: %w", err)
	}
	if !found || realUserSID == "" {
		return nil, fmt.Errorf("resolve real user SID: account %q not found", realUser)
	}
	sddl := "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + realUserSID + ")"
	securityDescriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, fmt.Errorf("build marker security descriptor: %w", err)
	}
	return securityDescriptor, nil
}
