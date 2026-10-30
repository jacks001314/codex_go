//go:build !unix

package processgroup

// Terminate is a no-op outside Unix, mirroring Rust's non-Unix stub.
func Terminate(pgid int) (bool, error) {
	return false, nil
}

// Kill is a no-op outside Unix, mirroring Rust's non-Unix stub.
func Kill(pgid int) error {
	return nil
}

// Interrupt is a no-op outside Unix, mirroring Rust's non-Unix stub.
func Interrupt(pgid int) error {
	return nil
}
