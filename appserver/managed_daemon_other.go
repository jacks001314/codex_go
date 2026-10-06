//go:build !unix

package appserver

// raiseManagedDaemonNoFileLimit is a no-op outside Unix, where the managed
// app-server daemon's control socket is not used (Rust #51470 is Unix-only).
func raiseManagedDaemonNoFileLimit() error {
	return nil
}
