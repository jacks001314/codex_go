//go:build windows

package mxc

import (
	"math"
	"sync"

	"golang.org/x/sys/windows"
)

var (
	osBuildNumberOnce  sync.Once
	osBuildNumberValue uint32
)

// OSBuildNumber returns the real (manifest-independent) OS build number, cached
// for the process lifetime, mirroring mxc-sdk's `os_build_number()`.
//
// `GetVersionExW` reports a shimmed version on post-8.1 builds, so the SDK loads
// `RtlGetVersion` from ntdll instead and returns u32::MAX when the probe fails;
// capability gating treats that as "modern" so an indeterminate probe keeps the
// more restrictive flag.
func OSBuildNumber() uint32 {
	osBuildNumberOnce.Do(func() {
		osBuildNumberValue = probeOSBuildNumber()
	})
	return osBuildNumberValue
}

// probeOSBuildNumber is split out so a missing ntdll export cannot take down the
// launcher: the SDK's failure mode is a sentinel, not a panic.
func probeOSBuildNumber() (build uint32) {
	defer func() {
		if recover() != nil {
			build = math.MaxUint32
		}
	}()
	info := windows.RtlGetVersion()
	if info == nil || info.BuildNumber == 0 {
		return math.MaxUint32
	}
	return info.BuildNumber
}
