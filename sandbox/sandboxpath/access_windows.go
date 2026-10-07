//go:build windows

package sandboxpath

// CurrentUserCanModify mirrors the Linux rule for Windows, where bubblewrap
// discovery never runs. An unverified path stays untrusted, so the conservative
// answer is that the current user may modify it.
func CurrentUserCanModify(path string) bool {
	return true
}
