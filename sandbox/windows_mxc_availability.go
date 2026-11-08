package sandbox

import (
	"runtime"
	"strconv"
	"sync/atomic"

	"codex_go/metrics"
)

// WindowsMXCAvailableMetric reports whether the native Windows MXC process
// security environment can be used on this host. Rust records it at most once
// per process when selecting a sandbox (#42094).
const WindowsMXCAvailableMetric = "codex.windows_mxc.available"

var windowsMXCAvailabilityRecorded atomic.Bool

// windowsMXCLaunchProbe is registered by sandbox/mxc so availability has a
// single source of truth: the native launcher's own PSEC probe. The sandbox
// package cannot import sandbox/mxc (mxc imports sandbox), so the probe is
// injected instead of duplicated.
var windowsMXCLaunchProbe atomic.Pointer[func() bool]

// RegisterWindowsMXCAvailabilityProbe lets the native MXC launcher publish its
// availability. Registering keeps WindowsMXCAvailable aligned with what
// launch() can actually do; without a registration the sandbox reports MXC as
// unavailable rather than claiming a backend it cannot start.
func RegisterWindowsMXCAvailabilityProbe(probe func() bool) {
	if probe == nil {
		windowsMXCLaunchProbe.Store(nil)
		return
	}
	windowsMXCLaunchProbe.Store(&probe)
}

// WindowsMXCAvailable reports whether the native MXC process security
// environment can be used on this host, as reported by the launcher's PSEC
// probe.
func WindowsMXCAvailable() bool {
	probe := windowsMXCLaunchProbe.Load()
	if probe == nil {
		return false
	}
	return (*probe)()
}

// recordMXCAvailabilityOnce mirrors Rust windows_mxc::record_availability_once.
// The tag carries the launcher's own PSEC probe result, so a host that cannot
// create a process security environment reports available=false.
func recordMXCAvailabilityOnce() {
	if runtime.GOOS != "windows" || metrics.Global() == nil {
		return
	}
	if windowsMXCAvailabilityRecorded.CompareAndSwap(false, true) {
		metrics.Counter(WindowsMXCAvailableMetric, 1, map[string]string{"available": strconv.FormatBool(WindowsMXCAvailable())})
	}
}
