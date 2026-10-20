//go:build linux

package rollout

import (
	"os"
	"syscall"
)

type rolloutChangeTime struct {
	sec  int64
	nsec int64
}

func rolloutInfoChangeTime(info os.FileInfo) (rolloutChangeTime, bool) {
	if info == nil {
		return rolloutChangeTime{}, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return rolloutChangeTime{}, false
	}
	return rolloutChangeTime{sec: stat.Ctim.Sec, nsec: stat.Ctim.Nsec}, true
}
