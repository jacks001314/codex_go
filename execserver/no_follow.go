package execserver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// noFollowSymlinkComponent walks every component of the resolved path and
// returns the first component that is a symlink (Rust #39659 no-follow
// filesystem operations). It rejects links in any path component and leaves
// regular-file access untouched.
func noFollowSymlinkComponent(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("no-follow path is empty")
	}
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	separator := string(os.PathSeparator)
	parts := strings.Split(rest, separator)
	current := volume
	if current == "" {
		current = separator
	}
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return current, nil
		}
	}
	return "", nil
}
