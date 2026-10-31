//go:build windows

package windowssandbox

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DirectoryOpenDisposition selects whether a no-reparse directory open may
// create the final directory (Rust #42342 DirectoryOpenDisposition).
type DirectoryOpenDisposition uint32

const (
	DirectoryOpenExisting DirectoryOpenDisposition = windows.FILE_OPEN
	DirectoryOpenOrCreate DirectoryOpenDisposition = windows.FILE_OPEN_IF
)

// ValidateLocalDirectoryPath mirrors Rust validate_local_directory_path: an
// absolute local-disk directory without relative components or alternate data
// streams. Validation never touches the filesystem, so it cannot follow a
// reparse point.
func ValidateLocalDirectoryPath(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("directory path must be absolute: %s", path)
	}
	_, rest, ok := splitLocalDiskVolume(path)
	if !ok {
		return fmt.Errorf("directory path must have a local disk prefix: %s", path)
	}
	for _, component := range strings.FieldsFunc(rest, isLocalPathSeparator) {
		switch component {
		case "", ".":
		case "..":
			return fmt.Errorf("directory path cannot contain relative or nested prefix components: %s", path)
		default:
			if strings.Contains(component, ":") {
				return fmt.Errorf("directory path cannot contain alternate data streams: %s", path)
			}
		}
	}
	return nil
}

func isLocalPathSeparator(unit rune) bool {
	return unit == '\\' || unit == '/'
}

// splitLocalDiskVolume accepts drive ("C:\...") and verbatim-drive
// ("\\?\C:\...") spellings only; device and UNC prefixes are
// not provisioning targets.
func splitLocalDiskVolume(path string) (volume string, rest string, ok bool) {
	if strings.HasPrefix(path, `\\?\`) {
		drive := path[4:]
		if len(drive) >= 2 && isASCIIDriveLetter(drive[0]) && drive[1] == ':' &&
			(len(drive) == 2 || isLocalPathSeparator(rune(drive[2]))) {
			return path[:6], drive[2:], true
		}
		return "", "", false
	}
	if len(path) >= 2 && isASCIIDriveLetter(path[0]) && path[1] == ':' &&
		(len(path) == 2 || isLocalPathSeparator(rune(path[2]))) {
		return path[:2], path[2:], true
	}
	return "", "", false
}

func isASCIIDriveLetter(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

// OpenDirectoryNoReparse opens a directory through NtCreateFile with
// OBJ_DONT_REPARSE, so every existing path component must be a real directory.
// Keep the handle for handle-based security mutations.
func OpenDirectoryNoReparse(path string, desiredAccess uint32, shareAccess uint32, disposition DirectoryOpenDisposition) (windows.Handle, error) {
	if err := ValidateLocalDirectoryPath(path); err != nil {
		return windows.InvalidHandle, err
	}
	name, err := ntPathUTF16(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	handle, err := openNoReparse(0, name, desiredAccess, shareAccess, uint32(disposition), windows.FILE_DIRECTORY_FILE)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("open directory %s: %w", path, err)
	}
	return handle, nil
}

// CreateDirectoryGuard keeps a directory nonempty so it cannot be converted to
// a reparse point while the returned delete-on-close handle is held. Creation is
// relative to the pinned directory, never to its mutable pathname.
func CreateDirectoryGuard(directory windows.Handle) (windows.Handle, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return windows.InvalidHandle, fmt.Errorf("generate sandbox directory guard name: %w", err)
	}
	name := fmt.Sprintf(".codex-provisioning-%016x.guard", binary.LittleEndian.Uint64(random[:]))
	nameUTF16, err := windows.UTF16FromString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	handle, err := openNoReparse(
		directory,
		nameUTF16,
		windows.FILE_READ_DATA|windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_CREATE,
		windows.FILE_NON_DIRECTORY_FILE|windows.FILE_DELETE_ON_CLOSE,
	)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("create sandbox directory guard: %w", err)
	}
	return handle, nil
}

func ntPathUTF16(path string) ([]uint16, error) {
	trimmed := path
	if strings.HasPrefix(trimmed, `\\?\`) {
		trimmed = trimmed[4:]
	}
	if len(trimmed) < 2 || trimmed[1] != ':' || !isASCIIDriveLetter(trimmed[0]) {
		return nil, fmt.Errorf("unsupported local path: %s", path)
	}
	nt := `\??\` + strings.ReplaceAll(trimmed, "/", `\`)
	units, err := windows.UTF16FromString(nt)
	if err != nil {
		return nil, err
	}
	return units, nil
}

func openNoReparse(root windows.Handle, name []uint16, desiredAccess uint32, shareAccess uint32, disposition uint32, options uint32) (windows.Handle, error) {
	return openNoReparseWithSecurityDescriptor(root, name, desiredAccess, shareAccess, disposition, options, nil)
}

// openNoReparseWithSecurityDescriptor creates the entry with a creation-time
// security descriptor so a fresh file never exists with inheritable default
// permissions (Rust #42342 prepare_setup_marker SDDL).
func openNoReparseWithSecurityDescriptor(root windows.Handle, name []uint16, desiredAccess uint32, shareAccess uint32, disposition uint32, options uint32, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	if len(name) == 0 || name[len(name)-1] != 0 {
		return windows.InvalidHandle, errors.New("no-reparse path must be NUL terminated")
	}
	nameLength := (len(name) - 1) * 2
	if nameLength > 0xFFFF {
		return windows.InvalidHandle, errors.New("no-reparse path is too long")
	}
	objectName := windows.NTUnicodeString{
		Length:        uint16(nameLength),
		MaximumLength: uint16(len(name) * 2),
		Buffer:        &name[0],
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:             uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory:      root,
		ObjectName:         &objectName,
		Attributes:         windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
		SecurityDescriptor: sd,
	}
	var ioStatus windows.IO_STATUS_BLOCK
	var handle windows.Handle
	err := windows.NtCreateFile(
		&handle,
		desiredAccess,
		&attributes,
		&ioStatus,
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		shareAccess,
		disposition,
		options,
		0,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, noReparseStatusError(err)
	}
	if handle == 0 || handle == windows.InvalidHandle {
		return windows.InvalidHandle, errors.New("NtCreateFile returned an invalid handle")
	}
	return handle, nil
}

func noReparseStatusError(err error) error {
	var status windows.NTStatus
	if errors.As(err, &status) {
		if status == windows.STATUS_REPARSE_POINT_ENCOUNTERED {
			return errors.New("path contains a reparse point")
		}
		return status.Errno()
	}
	return err
}
