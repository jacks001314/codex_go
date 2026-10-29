//go:build unix && !darwin

package processgroup

import (
	"errors"
	"syscall"
)

// Terminate sends SIGTERM to the process group, reporting whether an existing
// group accepted it. A group that is already gone reports (false, nil).
func Terminate(pgid int) (bool, error) {
	return signalGroup(pgid, syscall.SIGTERM)
}

// Kill sends SIGKILL to the process group (best-effort). A group that is
// already gone reports no error.
func Kill(pgid int) error {
	_, err := signalGroup(pgid, syscall.SIGKILL)
	return err
}

func signalGroup(pgid int, sig syscall.Signal) (bool, error) {
	if pgid <= 0 {
		return false, ErrInvalidProcessGroupID
	}
	err := syscall.Kill(-pgid, sig)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}
