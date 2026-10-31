//go:build !windows

package windowssandbox

// readSandboxAccountFlags never requests repair off Windows, where the managed
// sandbox accounts do not exist.
func readSandboxAccountFlags(string) (uint32, bool, error) {
	return 0, true, nil
}
