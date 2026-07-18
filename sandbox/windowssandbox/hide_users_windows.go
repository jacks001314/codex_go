//go:build windows

package windowssandbox

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func hideNewlyCreatedUsers(usernames []string, logBase string) error {
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, UserListRegistryPath, registry.CREATE_SUB_KEY|registry.SET_VALUE)
	if err != nil {
		logHideUsers(logBase, fmt.Sprintf("hide users: failed to update Winlogon UserList: %v", err))
		return err
	}
	defer key.Close()
	for _, username := range usernames {
		if username == "" {
			continue
		}
		if err := key.SetDWordValue(username, 0); err != nil {
			logHideUsers(logBase, fmt.Sprintf("hide users: failed to set UserList value for %s: %v", username, err))
		}
	}
	return nil
}

func hideCurrentUserProfileDir(logBase string) error {
	profileDir := os.Getenv("USERPROFILE")
	if profileDir == "" {
		return nil
	}
	if _, err := os.Stat(profileDir); err != nil {
		return nil
	}
	changed, err := HideDirectory(profileDir)
	if err != nil {
		logHideUsers(logBase, fmt.Sprintf("hide users: failed to hide current user profile dir (%s): %v", profileDir, err))
		return err
	}
	if changed {
		logHideUsers(logBase, fmt.Sprintf("hide users: profile dir hidden for current user (%s)", profileDir))
	}
	return nil
}

func HideDirectory(path string) (bool, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	attrs, err := windows.GetFileAttributes(name)
	if err != nil {
		return false, fmt.Errorf("GetFileAttributesW failed for %s: %w", path, err)
	}
	newAttrs := attrs | windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_SYSTEM
	if newAttrs == attrs {
		return false, nil
	}
	if err := windows.SetFileAttributes(name, newAttrs); err != nil {
		return false, fmt.Errorf("SetFileAttributesW failed for %s: %w", path, err)
	}
	return true, nil
}

func logHideUsers(logBase string, note string) {
	if logBase == "" {
		return
	}
	_ = LogNoteInDir(logBase, note)
}
