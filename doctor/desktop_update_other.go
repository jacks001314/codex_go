//go:build !windows

package doctor

import "net/http"

// desktopUpdatesSupported reports whether the platform has a desktop update
// channel Rust's doctor probes; only Windows is ported today.
func desktopUpdatesSupported() bool { return false }

func applyDesktopUpdateDetails(_ []*DoctorCheck, _ *http.Client) {}
