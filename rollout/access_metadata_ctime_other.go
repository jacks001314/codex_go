//go:build !linux && !darwin

package rollout

import "os"

type rolloutChangeTime struct{}

func rolloutInfoChangeTime(os.FileInfo) (rolloutChangeTime, bool) {
	return rolloutChangeTime{}, false
}
