//go:build linux || android

package execserver

import "golang.org/x/sys/unix"

// hardenRegistrationBearerRead denies same-user access to the process before a
// credential is read from the parent pipe (Rust #59254
// `set_dumpable_behavior(NotDumpable)`).
func hardenRegistrationBearerRead() error {
	if _, err := unix.PrctlRetInt(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return err
	}
	return nil
}
