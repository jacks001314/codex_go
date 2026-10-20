//go:build !darwin

package rollout

// copyPlatformRolloutACL is a no-op on platforms whose access metadata is
// fully represented by the ownership, mode, and mtime copied by the caller.
func copyPlatformRolloutACL(string, string) error {
	return nil
}
