//go:build !linux && !windows && !darwin

package execserver

import "os"

func createdAtMillisForPath(info os.FileInfo, _ string) int64 {
	return createdAtMillis(info)
}

// createdAtMillis falls back to zero on platforms without a birth-time
// helper (Rust #39666; darwin has its own implementation).
func createdAtMillis(info os.FileInfo) int64 {
	_ = info
	return 0
}
