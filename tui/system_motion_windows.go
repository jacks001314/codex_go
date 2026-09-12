//go:build windows

package tui

import (
	"syscall"
	"unsafe"
)

// spiGetClientAreaAnimation mirrors Rust's SPI_GETCLIENTAREAANIMATION
// (windows-sys); it reports whether client-area animations are enabled.
const spiGetClientAreaAnimation = 0x1042

var systemParametersInfoW = syscall.NewLazyDLL("user32.dll").NewProc("SystemParametersInfoW")

// platformSystemMotion reads the Windows client-area animation preference.
// It reports false when the query is unavailable.
func platformSystemMotion() (MotionMode, bool) {
	var enabled uint32
	ret, _, _ := systemParametersInfoW.Call(
		uintptr(spiGetClientAreaAnimation),
		0,
		uintptr(unsafe.Pointer(&enabled)),
		0,
	)
	if ret == 0 {
		return MotionAnimated, false
	}
	return MotionModeFromAnimationsEnabled(enabled != 0), true
}
