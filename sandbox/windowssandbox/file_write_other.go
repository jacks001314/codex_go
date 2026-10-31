//go:build !windows

package windowssandbox

import "os"

// WriteFileAtomically falls back to a direct write where the Windows
// handle-relative replacement is unavailable.
func WriteFileAtomically(path string, contents []byte) error {
	return os.WriteFile(path, contents, 0o600)
}
