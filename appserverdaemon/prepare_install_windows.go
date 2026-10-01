//go:build windows

package appserverdaemon

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// fsctlSetReparsePoint is the mount-point reparse operation the standalone
	// installer uses as well.
	fsctlSetReparsePoint   = 0x0009_00a4
	ioReparseTagMountPoint = 0xa000_0003
)

// selectDaemonRelease publishes release as the daemon's `current` junction,
// retargeting it in place so concurrent readers keep a usable selection
// (Rust prepare_install_windows::select_release).
func selectDaemonRelease(root, release string) error {
	canonical, err := filepath.EvalSymlinks(release)
	if err != nil {
		return fmt.Errorf("failed to resolve daemon release %s: %w", release, err)
	}
	current := filepath.Join(root, "current")
	if !pathExistsNoFollow(current) {
		temporary, err := os.MkdirTemp(root, stagingDirPrefix)
		if err != nil {
			return fmt.Errorf("failed to stage the daemon selection in %s: %w", root, err)
		}
		defer os.RemoveAll(temporary)
		junction := filepath.Join(temporary, "current")
		if err := os.Mkdir(junction, 0o755); err != nil {
			return fmt.Errorf("failed to create the daemon junction %s: %w", junction, err)
		}
		if err := retargetJunction(junction, canonical); err != nil {
			return err
		}
		if err := os.Rename(junction, current); err != nil {
			return fmt.Errorf("failed to publish the daemon selection %s: %w", current, err)
		}
		return nil
	}
	if err := validateDaemonSelection(root); err != nil {
		return err
	}
	return retargetJunction(current, canonical)
}

// validateDaemonSelection refuses to replace a selection that does not name a
// release inside the daemon's own releases directory.
func validateDaemonSelection(root string) error {
	current := filepath.Join(root, "current")
	if !pathExistsNoFollow(current) {
		return nil
	}
	resolved, err := resolveFinalPath(current)
	if err != nil {
		return fmt.Errorf("failed to resolve the daemon selection %s: %w", current, err)
	}
	releases, err := resolveFinalPath(filepath.Join(root, releasesDirName))
	if err != nil {
		return fmt.Errorf("failed to resolve the daemon releases directory: %w", err)
	}
	if filepath.Clean(filepath.Dir(resolved)) != filepath.Clean(releases) {
		return errors.New("refusing to replace a daemon selection outside its releases directory")
	}
	return nil
}

// retargetJunction points current at release with the same mount-point reparse
// operation the standalone installer uses.
func retargetJunction(current, release string) error {
	data, err := junctionReparseData(release)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(current)
	if err != nil {
		return fmt.Errorf("failed to resolve the daemon junction %s: %w", current, err)
	}
	handle, err := windows.CreateFile(
		target,
		windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return fmt.Errorf("failed to open the daemon junction %s: %w", current, err)
	}
	defer windows.CloseHandle(handle)
	var returned uint32
	if err := windows.DeviceIoControl(
		handle,
		fsctlSetReparsePoint,
		(*byte)(unsafe.Pointer(&data[0])),
		uint32(len(data)),
		nil,
		0,
		&returned,
		nil,
	); err != nil {
		return fmt.Errorf("failed to retarget managed daemon junction %s: %w", current, err)
	}
	return nil
}

// junctionReparseData builds the REPARSE_DATA_BUFFER that points at release:
// an 8-byte header, then the four u16 offsets/lengths of the intermediate
// buffer, then the UTF-16 substitute name. The print name stays empty.
func junctionReparseData(release string) ([]byte, error) {
	units := utf16.Encode([]rune(`\??\` + strings.TrimPrefix(release, `\\?\`)))
	path := make([]byte, 0, len(units)*2)
	for _, unit := range units {
		path = append(path, byte(unit), byte(unit>>8))
	}
	if len(path) > 0xffff-20 {
		return nil, errors.New("managed release path is too long")
	}
	length := uint16(len(path))
	data := make([]byte, len(path)+20)
	binary.LittleEndian.PutUint32(data[0:4], ioReparseTagMountPoint)
	binary.LittleEndian.PutUint16(data[4:6], length+12)
	binary.LittleEndian.PutUint16(data[10:12], length)
	binary.LittleEndian.PutUint16(data[12:14], length+2)
	copy(data[16:], path)
	return data, nil
}
