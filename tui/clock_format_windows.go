//go:build windows

package tui

import (
	"syscall"
	"unsafe"
)

const localeSShortTime = 0x00000079

var getLocaleInfoEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetLocaleInfoEx")

func detectSystemClockFormat() (ClockFormat, bool) {
	length, _, _ := getLocaleInfoEx.Call(
		0,
		localeSShortTime,
		0,
		0,
	)
	if length <= 1 {
		return ClockFormatTwelveHour, false
	}
	buffer := make([]uint16, length)
	written, _, _ := getLocaleInfoEx.Call(
		0,
		localeSShortTime,
		uintptr(unsafe.Pointer(&buffer[0])),
		length,
	)
	if written <= 1 {
		return ClockFormatTwelveHour, false
	}
	return parseNativeClockPattern(syscall.UTF16ToString(buffer[:written-1]), "hH")
}
