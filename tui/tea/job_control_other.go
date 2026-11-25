//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly

package tea

import "fmt"

func openJobControlSession() (jobControlSession, error) {
	return nil, fmt.Errorf("job control is unavailable on this platform")
}
