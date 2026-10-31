//go:build windows

package windowssandbox

// readSandboxAccountFlags reports a managed account's USER_INFO_1 flags. A
// missing account is not an error so the caller can repair it (Rust #46239
// read_local_user_flags).
func readSandboxAccountFlags(name string) (uint32, bool, error) {
	return readCleanupLocalUserFlags(name)
}
