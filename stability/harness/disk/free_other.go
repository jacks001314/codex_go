//go:build !windows && !linux && !darwin

package disk

import "errors"

// FreeBytes is unavailable on this platform.
func FreeBytes(string) (uint64, error) {
	return 0, errors.New("disk: free space is not implemented on this platform")
}
