//go:build windows

package windowsandboxservice

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox"
)

// resolveRestoredHome ports Rust installation_record::resolve_restored_home:
// a saved home that names a directory junction is migrated to its physical
// target, but only while that target stays pinned and its sandbox accounts
// still validate. The saved spelling is kept when it needs no migration, when
// the runtime is retiring, or when the home cannot be resolved.
func resolveRestoredHome(
	saved string,
	runtime *windowssandbox.RuntimeRegistration,
	pins *[]windows.Handle,
) (string, bool, error) {
	home, err := canonicalRestoredHome(saved)
	if err != nil {
		return "", false, nil
	}
	if filepath.Clean(home) == filepath.Clean(saved) || (runtime != nil && runtime.Retiring != nil) {
		return "", false, nil
	}
	if err := pinExistingAncestors(home, pins); err != nil {
		return "", false, err
	}
	homePin, err := pinDirectory(
		home,
		fileAddFile|fileAddSubdirectory|windows.WRITE_DAC,
		windowssandbox.DirectoryOpenExisting,
	)
	if err != nil {
		return "", false, err
	}
	*pins = append(*pins, homePin)
	if runtime == nil || len(runtime.Accounts) == 0 {
		return home, true, nil
	}
	if err := pinExistingAncestors(windowssandbox.SandboxSecretsDir(home), pins); err != nil {
		return "", false, err
	}
	for _, entry := range runtime.Accounts {
		loggedOn, err := runtimeLogonExistingAccount(home, entry.Account)
		if err != nil {
			return "", false, err
		}
		_, validateErr := ValidateRuntimeTarget(loggedOn, entry.Account, entry.UserSID)
		_ = loggedOn.Close()
		if validateErr != nil {
			return "", false, validateErr
		}
	}
	return home, true, nil
}

// canonicalRestoredHome resolves a directory junction the way Rust's
// Path::canonicalize does. Go's filepath.EvalSymlinks leaves junctions in place,
// so the physical path is read from an opened directory handle instead.
func canonicalRestoredHome(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	name, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		name,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)
	resolved, err := finalDirectoryPath(handle)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(resolved, `\\?\UNC\`) {
		resolved = `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`)
	} else {
		resolved = strings.TrimPrefix(resolved, `\\?\`)
	}
	if resolved == "" {
		return "", errors.New("resolved Codex home is empty")
	}
	return filepath.Clean(resolved), nil
}
