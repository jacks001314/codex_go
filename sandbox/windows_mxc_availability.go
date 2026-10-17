package sandbox

import (
	"runtime"
	"sync/atomic"

	"codex_go/metrics"
)

// WindowsMXCAvailableMetric reports whether the native Windows MXC process
// security environment can be used on this host. Rust records it at most once
// per process when selecting a sandbox (#42094).
const WindowsMXCAvailableMetric = "codex.windows_mxc.available"

var windowsMXCAvailabilityRecorded atomic.Bool

// recordMXCAvailabilityOnce mirrors Rust windows_mxc::record_availability_once.
// Go has no native MXC launcher, so the truthful availability value is false;
// the signal remains useful for comparing deployment capability across hosts.
func recordMXCAvailabilityOnce() {
	if runtime.GOOS != "windows" || metrics.Global() == nil {
		return
	}
	if windowsMXCAvailabilityRecorded.CompareAndSwap(false, true) {
		metrics.Counter(WindowsMXCAvailableMetric, 1, map[string]string{"available": "false"})
	}
}
