package appserver

import (
	"os"
	"syscall"
	"time"
)

func createdAtMillis(info os.FileInfo) int64 {
	if info == nil || info.Sys() == nil {
		return 0
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return 0
	}
	return unixMillis(time.Unix(0, data.CreationTime.Nanoseconds()))
}
