//go:build darwin

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
	return rolloutChangeTime{sec: stat.Ctimespec.Sec, nsec: stat.Ctimespec.Nsec}, true
}
