package elevated

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

var ErrWindowsOnly = errors.New("windows sandbox elevated support is only available on Windows")

func unsupported(feature string) error {
	feature = strings.TrimSpace(feature)
	if runtime.GOOS != "windows" {
		if feature == "" {
			return ErrWindowsOnly
		}
		return fmt.Errorf("%w: %s", ErrWindowsOnly, feature)
	}
	if feature == "" {
		return errors.New("windows sandbox elevated backend is not implemented")
	}
	return fmt.Errorf("windows sandbox elevated backend is not implemented: %s", feature)
}
