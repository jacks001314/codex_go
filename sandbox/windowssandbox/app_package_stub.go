//go:build !windows

package windowssandbox

// CurrentProcessHasPackageIdentity mirrors the Windows check: a non-Windows
// process has no OS package identity, so no desktop app context is preserved.
func CurrentProcessHasPackageIdentity() (bool, error) {
	return false, nil
}

// CurrentProcessPackageFullName reports no package identity off Windows.
func CurrentProcessPackageFullName() (string, bool, error) {
	return "", false, nil
}

// CurrentProcessPackageFamilyName reports no package identity off Windows.
func CurrentProcessPackageFamilyName() (string, bool, error) {
	return "", false, nil
}
