//go:build !linux && !windows

package execserver

import "os"

func createdAtMillisForPath(info os.FileInfo, _ string) int64 {
	return createdAtMillis(info)
}
