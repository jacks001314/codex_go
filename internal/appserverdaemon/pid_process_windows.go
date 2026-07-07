//go:build windows

package appserverdaemon

import "fmt"

func startDetachedPIDProcess(_ *PIDBackend) (uint32, string, error) {
	return 0, "", fmt.Errorf("pid-managed app-server startup is unsupported on this platform")
}

func processMatchesPIDRecord(_ *PIDRecord) (bool, error) {
	return false, nil
}

func terminatePIDProcess(_ uint32) error {
	return fmt.Errorf("pid-managed app-server shutdown is unsupported on this platform")
}

func forceTerminatePIDProcess(_ uint32, _ bool) error {
	return fmt.Errorf("pid-managed app-server shutdown is unsupported on this platform")
}
