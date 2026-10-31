//go:build windows

package windowsandboxservice

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

const (
	driveFixed          = 3
	fileAddFile         = 0x0002
	fileAddSubdirectory = 0x0004
	fileTraverse        = 0x0020
	driveTypePathSuffix = `\`
)

// ServiceUnavailable marks a request the service cannot complete while the
// interactive setup helper still might (Rust ipc::ServiceUnavailable).
type ServiceUnavailable struct {
	Reason string
}

func (e *ServiceUnavailable) Error() string { return e.Reason }

func unavailable(reason string) error { return &ServiceUnavailable{Reason: reason} }

// IsServiceUnavailable reports whether err tells the client to fall back to
// its elevated setup helper.
func IsServiceUnavailable(err error) bool {
	var target *ServiceUnavailable
	return errors.As(err, &target)
}

// prepareCodexHome ports Rust ipc::home::prepare_codex_home: the requested home
// must live on a fixed local drive, every ancestor is pinned without following
// reparse points, and the sandbox leaves keep a directory guard so they cannot
// become junctions during provisioning. Registered Core pins three directories;
// the legacy runtime also pins .sandbox-bin.
func prepareCodexHome(requested string, registered bool, disposition windowssandbox.DirectoryOpenDisposition) (string, []windows.Handle, error) {
	directoryCount := 4
	if registered {
		directoryCount = 3
	}
	var handles []windows.Handle
	fail := func(err error) (string, []windows.Handle, error) {
		closeHandles(handles)
		return "", nil, err
	}
	if err := windowssandbox.ValidateLocalDirectoryPath(requested); err != nil {
		return fail(err)
	}
	if err := requireFixedDrive(requested); err != nil {
		return fail(err)
	}
	parent := filepath.Dir(requested)
	if parent == requested {
		return fail(errors.New("Codex home must have an existing parent directory"))
	}
	if err := pinExistingAncestors(parent, &handles); err != nil {
		return fail(err)
	}
	requestedRoot, err := pinDirectory(requested, windows.FILE_READ_ATTRIBUTES, disposition)
	if err != nil {
		return fail(err)
	}
	handles = append(handles, requestedRoot)
	home, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return fail(fmt.Errorf("canonicalize Codex home %s: %w", requested, err))
	}
	if err := windowssandbox.ValidateLocalDirectoryPath(home); err != nil {
		return fail(err)
	}
	if err := requireFixedDrive(home); err != nil {
		return fail(err)
	}
	if !strings.EqualFold(home, filepath.Clean(requested)) {
		if err := pinExistingAncestors(home, &handles); err != nil {
			return fail(err)
		}
	}
	sandboxBin := filepath.Join(home, ".sandbox-bin")
	directories := []string{
		home,
		filepath.Join(home, ".sandbox"),
		filepath.Join(home, ".sandbox-secrets"),
		sandboxBin,
	}
	for index, directory := range directories[:directoryCount] {
		access := uint32(windows.FILE_READ_ATTRIBUTES | fileAddFile | fileAddSubdirectory)
		if index != 0 {
			access |= windows.WRITE_DAC
		}
		handle, err := pinDirectory(directory, access, disposition)
		if err != nil {
			// Older elevated installs omitted WRITE_DAC; the interactive helper repairs it.
			if directory == sandboxBin && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				return fail(fmt.Errorf("sandbox binary directory needs elevated repair: %w", err))
			}
			if index == 0 {
				return fail(fmt.Errorf("requesting user must be permitted to write Codex home %s: %w", directory, err))
			}
			return fail(fmt.Errorf("requesting user must be permitted to write and modify sandbox directory ACL %s: %w", directory, err))
		}
		if index != 0 {
			guard, err := windowssandbox.CreateDirectoryGuard(handle)
			if err != nil {
				_ = windows.CloseHandle(handle)
				return fail(err)
			}
			// Reject conversion before guard creation; afterwards the retained file
			// keeps the directory nonempty.
			probe, err := pinDirectory(directory, windows.FILE_READ_ATTRIBUTES, windowssandbox.DirectoryOpenExisting)
			if err != nil {
				_ = windows.CloseHandle(guard)
				_ = windows.CloseHandle(handle)
				return fail(err)
			}
			_ = windows.CloseHandle(probe)
			handles = append(handles, guard)
		}
		handles = append(handles, handle)
		if index == 0 {
			continue
		}
		if err := rejectReparseEntries(directory); err != nil {
			return fail(err)
		}
	}
	// Pinned parent handles prevent directory replacement, but existing helper
	// file opens remain pathname-based; hardlink and leaf-file races need the
	// follow-up redesign Rust tracks.
	return home, handles, nil
}

// pinExistingAncestors pins every absolute ancestor without following reparse
// points (Rust pin_existing_ancestors).
func pinExistingAncestors(path string, handles *[]windows.Handle) error {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	if volume == "" {
		return fmt.Errorf("pin %s: not an absolute local path", path)
	}
	current := volume + string(filepath.Separator)
	if err := appendPinnedDirectory(current, handles); err != nil {
		return err
	}
	rest := strings.TrimPrefix(cleaned, current)
	for _, part := range strings.Split(rest, string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if err := appendPinnedDirectory(current, handles); err != nil {
			return err
		}
	}
	return nil
}

func appendPinnedDirectory(path string, handles *[]windows.Handle) error {
	handle, err := pinDirectory(path, windows.FILE_READ_ATTRIBUTES, windowssandbox.DirectoryOpenExisting)
	if err != nil {
		return err
	}
	*handles = append(*handles, handle)
	return nil
}

// pinDirectory opens a directory without following a final reparse point and
// adds FILE_TRAVERSE (Rust ipc::home::pin_directory).
func pinDirectory(path string, access uint32, disposition windowssandbox.DirectoryOpenDisposition) (windows.Handle, error) {
	handle, err := windowssandbox.OpenDirectoryNoReparse(
		path,
		access|fileTraverse,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		disposition,
	)
	if err != nil {
		return 0, fmt.Errorf("pin %s: %w", path, err)
	}
	return handle, nil
}

// requireFixedDrive refuses a Codex home on a network, removable or ramdisk
// volume (Rust GetDriveTypeW == DRIVE_FIXED).
func requireFixedDrive(path string) error {
	volume := filepath.VolumeName(path)
	if volume == "" {
		return fmt.Errorf("Codex home %s is not on a local drive", path)
	}
	root, err := windows.UTF16PtrFromString(volume + driveTypePathSuffix)
	if err != nil {
		return err
	}
	if windows.GetDriveType(root) != driveFixed {
		return unavailable("Codex home must be located on a fixed local drive")
	}
	return nil
}

// rejectReparseEntries refuses a pinned sandbox directory that already holds a
// reparse point.
func rejectReparseEntries(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing reparse point %s", filepath.Join(directory, entry.Name()))
		}
		info, err := os.Lstat(filepath.Join(directory, entry.Name()))
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing reparse point %s", filepath.Join(directory, entry.Name()))
		}
	}
	return nil
}

func closeHandles(handles []windows.Handle) {
	for _, handle := range handles {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
	}
}
