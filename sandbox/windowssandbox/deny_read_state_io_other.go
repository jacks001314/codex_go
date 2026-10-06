//go:build !windows

package windowssandbox

import "os"

// The deny-read ACL state belongs to the Windows sandbox; on other platforms
// the sync entry point is a stub, so these helpers only need to compile and
// support the cross-platform round-trip test.

func openDenyReadACLStateForRead(path string, stable bool) (*os.File, error) {
	return os.Open(path)
}

func openDenyReadACLStateForWrite(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o600)
}

func validateDenyReadACLStateFile(file *os.File) error {
	return nil
}

func isDenyReadStateSharingViolation(err error) bool {
	return false
}
