//go:build !darwin

package sandboxpath

// Windows comparisons already resolve case aliases through filepath.Rel.
// Other Unix filesystems are case-sensitive, so there is no alias to resolve.
func metadataCaseAlias(string, string) (string, bool) {
	return "", false
}
