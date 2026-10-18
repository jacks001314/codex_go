//go:build linux

package linuxsandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const wslgDistroRoot = "/mnt/wslg/distro"

// wslgDuplicateRootPresent reports whether /mnt/wslg/distro is the same
// directory as the primary root. An unrelated directory at that path is left
// alone (Rust #45837).
func wslgDuplicateRootPresent() (bool, error) {
	aliasInfo, err := os.Lstat(wslgDistroRoot)
	if err != nil {
		if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect WSLg duplicate root: %w", err)
	}
	rootInfo, err := os.Stat("/")
	if err != nil {
		return false, fmt.Errorf("failed to inspect primary root: %w", err)
	}
	aliasStat, aliasOK := aliasInfo.Sys().(*syscall.Stat_t)
	rootStat, rootOK := rootInfo.Sys().(*syscall.Stat_t)
	if !aliasOK || !rootOK {
		return false, nil
	}
	return aliasInfo.IsDir() && aliasStat.Dev == rootStat.Dev && aliasStat.Ino == rootStat.Ino, nil
}

func ensureNoWSLGDistroAlias(paths ...string) error {
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if cleaned == wslgDistroRoot || strings.HasPrefix(cleaned, wslgDistroRoot+string(filepath.Separator)) {
			return wslgAliasError(path)
		}
		if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
			resolved = filepath.Clean(resolved)
			if resolved == wslgDistroRoot || strings.HasPrefix(resolved, wslgDistroRoot+string(filepath.Separator)) {
				return wslgAliasError(path)
			}
		}
	}
	return nil
}

func wslgAliasError(path string) error {
	return fmt.Errorf("restricted sandboxes do not support paths under %s: %s; use the corresponding path under the primary filesystem root instead", wslgDistroRoot, path)
}

// wslgMaskArgs masks the duplicate root after all filesystem grants. An
// existing deny-read ancestor already hides it and cannot host a child mount.
func wslgMaskArgs(maskedByAncestor bool, noProc bool, wslInteropPresent bool) []string {
	if maskedByAncestor {
		return nil
	}
	args := []string{"--perms", "000", "--tmpfs", wslgDistroRoot, "--remount-ro", wslgDistroRoot}
	if noProc && !wslInteropPresent {
		args = append(args, "--tmpfs", "/proc")
	}
	return args
}
