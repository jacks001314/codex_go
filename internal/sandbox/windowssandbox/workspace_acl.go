package windowssandbox

import (
	"os"
	"path/filepath"
)

func IsCommandCWDRoot(commandCWD string, root string) bool {
	if commandCWD == "" || root == "" {
		return false
	}
	return CanonicalPathKey(commandCWD) == CanonicalPathKey(root)
}

func ProtectWorkspaceCodexDir(cwd string, sid string) (bool, error) {
	return protectWorkspaceSubdir(cwd, sid, ".codex")
}

func ProtectWorkspaceAgentsDir(cwd string, sid string) (bool, error) {
	return protectWorkspaceSubdir(cwd, sid, ".agents")
}

func protectWorkspaceSubdir(cwd string, sid string, subdir string) (bool, error) {
	if cwd == "" || sid == "" || subdir == "" {
		return false, ErrInvalidRequest
	}
	path := filepath.Join(cwd, subdir)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	if err := AddDenyWriteACE(ACLRequest{Path: path, SID: sid}); err != nil {
		return false, err
	}
	return true, nil
}
