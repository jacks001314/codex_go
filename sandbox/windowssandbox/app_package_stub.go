//go:build !windows

package windowssandbox

// CurrentProcessHasPackageIdentity mirrors the Windows check: a non-Windows
// process has no OS package identity, so no desktop app context is preserved.
func CurrentProcessHasPackageIdentity() (bool, error) {
	return false, nil
}
