//go:build !linux

package processgroup

// HideInheritedDescriptors is Linux-only: the descriptor sweep is how Go
// reproduces Rust's close_inherited_fds_except (#47605). macOS uses
// POSIX_SPAWN_CLOEXEC_DEFAULT there, which os/exec cannot request, so other
// platforms keep their native inheritance behaviour.
func HideInheritedDescriptors() (restore func()) {
	return func() {}
}
