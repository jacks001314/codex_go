//go:build !windows

package windowsandboxservice

import "errors"

// ServiceUnavailable marks a request the service cannot complete while the
// interactive setup helper still might.
type ServiceUnavailable struct {
	Reason string
}

func (e *ServiceUnavailable) Error() string { return e.Reason }

func unavailable(reason string) error { return &ServiceUnavailable{Reason: reason} }

// IsServiceUnavailable reports whether err tells the client to fall back to
// its elevated setup helper.
func IsServiceUnavailable(err error) bool {
	var target *ServiceUnavailable
	return errors.As(err, &target)
}
