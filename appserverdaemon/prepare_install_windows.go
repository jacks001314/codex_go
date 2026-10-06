//go:build windows

package appserverdaemon

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
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
	return selectDaemonReleaseWith(root, release, retargetJunction)
}

// selectDaemonReleaseWith is the injectable form of selectDaemonRelease: the
// retarget hook lets tests simulate a Windows policy that denies in-process
// reparse-point mutation (Rust #50802 select_release_with). Native creation or
// retargeting that returns PermissionDenied falls back to the system junction
// creator (`cmd.exe`'s `mklink /J`).
func selectDaemonReleaseWith(root, release string, retarget func(current, release string) error) error {
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
		if err := retarget(junction, canonical); err != nil {
			if !isPermissionDenied(err) {
				return err
			}
			if installErr := installJunction(root, canonical); installErr != nil {
				return fmt.Errorf("failed to create managed daemon junction after native creation was denied: %v: %w", err, installErr)
			}
			return nil
		}
		if err := os.Rename(junction, current); err != nil {
			return fmt.Errorf("failed to publish the daemon selection %s: %w", current, err)
		}
		return nil
	}
	if err := validateDaemonSelection(root); err != nil {
		return err
	}
	if err := retarget(current, canonical); err != nil {
		if !isPermissionDenied(err) {
			return err
		}
		if installErr := installJunction(root, canonical); installErr != nil {
			return fmt.Errorf("failed to replace managed daemon junction after retargeting was denied: %v: %w", err, installErr)
		}
	}
	return nil
}

// isPermissionDenied reports whether the failure is an access denial, which a
// Windows policy may allow only via the system junction creator (Rust #50802
// is_permission_denied).
func isPermissionDenied(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// installJunction creates the daemon's `current` selection through `cmd.exe`'s
// `mklink /J`, for hosts where in-process reparse-point mutation is denied but
// the system junction creator is allowed (Rust #50802 install_junction). Paths
// travel through environment variables and delayed expansion is disabled so
// shell metacharacters stay quoted.
func installJunction(root, release string) error {
	temporary, err := os.MkdirTemp(root, stagingDirPrefix)
	if err != nil {
		return fmt.Errorf("failed to stage the managed daemon junction in %s: %w", root, err)
	}
	preserve := false
	defer func() {
		if !preserve {
			_ = os.RemoveAll(temporary)
		}
	}()
	junction := filepath.Join(temporary, "current")
	current := filepath.Join(root, "current")
	replacing := pathExistsNoFollow(current)
	if replacing {
		if err := validateDaemonSelection(root); err != nil {
			return err
		}
	}
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		return errors.New("SystemRoot is not set")
	}
	commandShell := filepath.Join(systemRoot, "System32", "cmd.exe")
	if !filepath.IsAbs(commandShell) {
		return errors.New("SystemRoot must be an absolute path")
	}
	if _, err := os.Stat(commandShell); err != nil {
		return fmt.Errorf("cmd.exe is unavailable at %s: %w", commandShell, err)
	}
	cmd := exec.Command(commandShell)
	cmd.Env = append(os.Environ(),
		"CODEX_DAEMON_JUNCTION_LINK="+junction,
		"CODEX_DAEMON_JUNCTION_TARGET="+release,
	)
	// The raw command line mirrors Rust's `raw_arg`: cmd.exe's `/s` strips the
	// outer quotes and runs the rest verbatim, so the inner quotes reach mklink.
	cmd.SysProcAttr = &windows.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
		CmdLine:       `"` + commandShell + `" /d /s /e:on /v:off /c "mklink /J "%CODEX_DAEMON_JUNCTION_LINK%" "%CODEX_DAEMON_JUNCTION_TARGET%""`,
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cmd.exe could not create managed daemon junction: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if !replacing {
		if err := os.Rename(junction, current); err != nil {
			return fmt.Errorf("failed to publish the daemon selection %s: %w", current, err)
		}
		return nil
	}
	previous := filepath.Join(temporary, "previous")
	if err := os.Rename(current, previous); err != nil {
		return fmt.Errorf("failed to move the previous daemon junction aside: %w", err)
	}
	if err := os.Rename(junction, current); err != nil {
		if restoreErr := os.Rename(previous, current); restoreErr != nil {
			// Keep the staged junction for recovery instead of removing it.
			preserve = true
			return fmt.Errorf("failed to replace managed daemon junction: %w; also failed to restore the previous junction from %s: %v", err, temporary, restoreErr)
		}
		return fmt.Errorf("failed to replace managed daemon junction: %w", err)
	}
	// os.Remove, not os.RemoveAll: the previous entry is a junction, and a
	// recursive removal could walk into the release it points at.
	if err := os.Remove(previous); err != nil {
		slog.Warn("failed to remove previous managed daemon junction", "error", err)
	}
	return nil
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
