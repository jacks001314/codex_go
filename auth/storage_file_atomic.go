package auth

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const maxCredentialSymlinks = 40

// resolveAuthFile follows a credential symlink chain so writers replace the
// target the link points at instead of splitting the link (Rust #58251
// storage_file::resolve_auth_file).
func resolveAuthFile(path string) (string, error) {
	for range maxCredentialSymlinks {
		info, err := os.Lstat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return path, nil
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return "", errors.New("too many credential symlinks")
}

// writeAuthFileAtomically replaces path with data so overlapping readers only
// ever observe a complete document and the file keeps its credential
// permissions (Rust #58251 storage_file).
func writeAuthFileAtomically(path string, data []byte) error {
	resolved, err := resolveAuthFile(path)
	if err != nil {
		return err
	}
	if err := checkAuthFileReplaceable(resolved); err != nil {
		return err
	}
	temp, err := createAuthFileReplacement(resolved)
	if err != nil {
		return err
	}
	tempName := temp.Name()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempName)
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		_ = os.Remove(tempName)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	if err := os.Rename(tempName, resolved); err != nil {
		_ = os.Remove(tempName)
		return err
	}
	return nil
}

// checkAuthFileReplaceable rejects destinations whose replacement would change
// file identity: read-only files and hard links fail instead of splitting
// (Rust #58251 check_replaceable).
func checkAuthFileReplaceable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode().Perm()&0o200 == 0 {
		return &os.PathError{Op: "replace", Path: path, Err: fmt.Errorf("credential file is read-only: %w", fs.ErrPermission)}
	}
	// Rust verifies the destination itself is writable, not only its parent.
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return checkAuthFileLinks(path)
}
