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
// reparse-free sandbox directory, installs its protected DACL on the handle, and
// retains the exclusive handle for the whole provisioning run
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
	handle, err := openNoReparse(
		directory,
		nameUTF16,
		windows.FILE_READ_DATA|windows.FILE_WRITE_DATA|windows.READ_CONTROL|windows.WRITE_DAC|windows.DELETE|windows.SYNCHRONIZE,
		0,
		windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_SYNCHRONOUS_IO_NONALERT,
	)
	if err != nil {
		return nil, fmt.Errorf("create protected setup marker file %s: %w", markerPath, err)
	}
	if err := applyProtectedMarkerDACL(handle, realUser); err != nil {
		_ = os.Remove(markerPath)
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("protect setup marker file %s: %w", markerPath, err)
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

func applyProtectedMarkerDACL(handle windows.Handle, realUser string) error {
	realUserSID, found, err := lookupLocalUserSID(realUser)
	if err != nil {
		return fmt.Errorf("resolve real user SID: %w", err)
	}
	if !found || realUserSID == "" {
		return fmt.Errorf("resolve real user SID: account %q not found", realUser)
	}
	entries, sids, err := markerDACLEntries(realUserSID)
	if err != nil {
		return err
	}
	dacl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	err = windows.SetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
	runtime.KeepAlive(entries)
	runtime.KeepAlive(sids)
	runtime.KeepAlive(dacl)
	if err != nil {
		return fmt.Errorf("set marker DACL: %w", err)
	}
	return nil
}

func markerDACLEntries(realUserSID string) ([]windows.EXPLICIT_ACCESS, []*windows.SID, error) {
	sidStrings := []string{"S-1-5-18", "S-1-5-32-544", realUserSID}
	entries := make([]windows.EXPLICIT_ACCESS, 0, len(sidStrings))
	sids := make([]*windows.SID, 0, len(sidStrings))
	for _, value := range sidStrings {
		sid, err := windows.StringToSid(value)
		if err != nil {
			return nil, nil, err
		}
		sids = append(sids, sid)
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       windows.NO_INHERITANCE,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	return entries, sids, nil
}
