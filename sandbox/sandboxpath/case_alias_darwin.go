//go:build darwin

package sandboxpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// macOS _PC_CASE_SENSITIVE. x/sys/unix does not export the Darwin constant.
const pcCaseSensitive = 100

// metadataCaseAlias resolves ASCII case aliases of a protected metadata
// component and of the writable root. An unavailable filesystem sensitivity
// answer reports an alias so the caller fails closed rather than granting a
// protected metadata write.
func metadataCaseAlias(path string, root string) (string, bool) {
	rootParts := pathComponents(root)
	pathParts := pathComponents(path)
	if len(rootParts) == 0 || len(pathParts) < len(rootParts) {
		return "", false
	}
	if len(pathParts) == len(rootParts) {
		return "", false
	}
	candidate := pathParts[len(rootParts)]
	metadataName := ""
	for _, name := range DefaultProtectedMetadataNames() {
		if strings.EqualFold(candidate, name) {
			metadataName = name
			break
		}
	}
	if metadataName == "" {
		return "", false
	}
	normalized := func() string {
		parts := append([]string(nil), rootParts...)
		parts = append(parts, metadataName)
		if len(pathParts) > len(rootParts)+1 {
			parts = append(parts, pathParts[len(rootParts)+1:]...)
		}
		return filepath.Join(parts...)
	}
	for index := range rootParts {
		expected, actual := rootParts[index], pathParts[index]
		if expected == actual {
			continue
		}
		if !strings.EqualFold(expected, actual) {
			return "", false
		}
		parent := filepath.Join(rootParts[:index]...)
		if parent == "" {
			parent = string(filepath.Separator)
		}
		insensitive, err := filesystemCaseInsensitive(parent)
		if err != nil || !insensitive {
			if err != nil {
				return normalized(), true
			}
			return "", false
		}
	}
	if candidate != metadataName {
		insensitive, err := filesystemCaseInsensitive(root)
		if err != nil {
			return normalized(), true
		}
		if !insensitive {
			return "", false
		}
	}
	return normalized(), true
}

func pathComponents(path string) []string {
	path = filepath.Clean(path)
	volume := filepath.VolumeName(path)
	path = strings.TrimPrefix(path, volume)
	absolute := strings.HasPrefix(path, string(filepath.Separator))
	path = strings.TrimPrefix(path, string(filepath.Separator))
	if path == "" || path == "." {
		if volume != "" {
			return []string{volume}
		}
		if absolute {
			return []string{string(filepath.Separator)}
		}
		return nil
	}
	parts := strings.Split(path, string(filepath.Separator))
	if volume != "" {
		parts = append([]string{volume}, parts...)
	}
	if absolute {
		parts = append([]string{string(filepath.Separator)}, parts...)
	}
	return parts
}

func filesystemCaseInsensitive(path string) (bool, error) {
	for candidate := filepath.Clean(path); ; candidate = filepath.Dir(candidate) {
		value, err := unix.Pathconf(candidate, pcCaseSensitive)
		switch {
		case err == nil && value == 0:
			return true, nil
		case err == nil && value == 1:
			return false, nil
		case err == nil:
			return false, errors.New("unexpected filesystem case sensitivity")
		case errors.Is(err, os.ErrNotExist):
			parent := filepath.Dir(candidate)
			if parent == candidate {
				return false, os.ErrNotExist
			}
		default:
			return false, err
		}
	}
}
