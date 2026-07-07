//go:build !windows

package execserver

import "os"

func createdAtMillis(info os.FileInfo) int64 {
	return 0
}
