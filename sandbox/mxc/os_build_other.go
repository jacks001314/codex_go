//go:build !windows

package mxc

import "math"

// OSBuildNumber reports an unknown build off Windows. The job-object UI-limit
// path only runs on Windows, so the value only affects diagnostics; reporting
// u32::MAX keeps the "unknown means modern" fail-secure convention.
func OSBuildNumber() uint32 {
	return math.MaxUint32
}
