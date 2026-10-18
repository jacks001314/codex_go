//go:build unix

package integrity

import (
	"os"
	"syscall"
)

func multipleHardLinksTag(info os.FileInfo) string {
	if info == nil {
		return "unknown"
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "unknown"
	}
	return boolTag(stat.Nlink > 1)
}
