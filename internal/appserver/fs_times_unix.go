//go:build !windows

package appserver

import "os"

func createdAtMillis(info os.FileInfo) int64 {
	return 0
}
