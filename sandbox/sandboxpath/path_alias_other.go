//go:build !darwin

package sandboxpath

// normalizePathForContainment preserves the platform path identity used by
// filepath.Rel. Windows already compares path components case-insensitively.
func normalizePathForContainment(path, root string) (string, string) {
	return path, root
}
