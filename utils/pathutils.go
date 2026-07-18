package utils

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type SymlinkWritePaths struct {
	ReadPath  *string
	WritePath string
}

func IsWSL() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func NormalizeForPathComparison(path string) (string, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(canonical)
	if err != nil {
		return "", err
	}
	return NormalizeForWSL(absolute, IsWSL()), nil
}

func PathsMatchAfterNormalization(left string, right string) bool {
	normalizedLeft, leftErr := NormalizeForPathComparison(left)
	normalizedRight, rightErr := NormalizeForPathComparison(right)
	if leftErr == nil && rightErr == nil {
		return normalizedLeft == normalizedRight
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func NormalizeForNativeWorkdir(path string) string {
	return NormalizeForNativeWorkdirWithFlag(path, runtime.GOOS == "windows")
}

func NormalizeForNativeWorkdirWithFlag(path string, isWindows bool) string {
	if !isWindows {
		return path
	}
	path = strings.TrimPrefix(path, `\\?\`)
	return filepath.Clean(path)
}

func NormalizeForWSL(path string, isWSL bool) string {
	if !isWSL || !isWSLCaseInsensitivePath(path) {
		return path
	}
	return strings.ToLower(path)
}

func ResolveSymlinkWritePaths(path string) (SymlinkWritePaths, error) {
	if path == "" {
		return SymlinkWritePaths{}, errors.New("path is required")
	}
	root, err := filepath.Abs(path)
	if err != nil {
		root = path
	}
	current := root
	visited := map[string]bool{}
	for {
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				read := current
				return SymlinkWritePaths{ReadPath: &read, WritePath: current}, nil
			}
			return SymlinkWritePaths{ReadPath: nil, WritePath: root}, nil
		}
		if info.Mode()&os.ModeSymlink == 0 {
			read := current
			return SymlinkWritePaths{ReadPath: &read, WritePath: current}, nil
		}
		if visited[current] {
			return SymlinkWritePaths{ReadPath: nil, WritePath: root}, nil
		}
		visited[current] = true
		target, err := os.Readlink(current)
		if err != nil {
			return SymlinkWritePaths{ReadPath: nil, WritePath: root}, nil
		}
		if filepath.IsAbs(target) {
			current = filepath.Clean(target)
		} else {
			current = filepath.Clean(filepath.Join(filepath.Dir(current), target))
		}
	}
}

func WriteAtomically(writePath string, contents string) error {
	parent := filepath.Dir(writePath)
	if parent == "." || parent == "" {
		return errors.New("path has no parent directory")
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			_ = tmp.Close()
		}
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.WriteString(contents); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		closed = true
		return err
	}
	closed = true
	if err := os.Rename(tmpName, writePath); err != nil {
		return err
	}
	return nil
}

func isWSLCaseInsensitivePath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	parts := strings.Split(clean, "/")
	if len(parts) < 4 || parts[0] != "" {
		return false
	}
	return strings.EqualFold(parts[1], "mnt") && len(parts[2]) == 1 && pathUtilsASCIIAlpha(parts[2][0])
}

func pathUtilsASCIIAlpha(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}
