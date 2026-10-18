//go:build linux && cgo

package tui

/*
#include <langinfo.h>
#include <locale.h>
#include <stdlib.h>
*/
import "C"

import (
	"os"
	"unsafe"
)

// detectSystemClockFormat reads the glibc LC_TIME pattern through an
// independent locale object, so detection neither changes the process locale
// nor depends on the current one.
func detectSystemClockFormat() (ClockFormat, bool) {
	locale := pickTimeLocale(os.Getenv)
	if locale == "" {
		return ClockFormatTwelveHour, false
	}
	return detectTimeLocale(locale)
}

func detectTimeLocale(locale string) (ClockFormat, bool) {
	if format, ok := standardTimeLocale(locale); ok {
		return format, true
	}
	name := C.CString(locale)
	defer C.free(unsafe.Pointer(name))
	handle := C.newlocale(C.LC_TIME_MASK, name, nil)
	if handle == nil {
		// An unavailable locale must not be mistaken for a 24-hour preference.
		return ClockFormatTwelveHour, false
	}
	defer C.freelocale(handle)
	pattern := C.nl_langinfo_l(C.T_FMT, handle)
	if pattern == nil {
		return ClockFormatTwelveHour, false
	}
	return parseStrftimeClockPattern(C.GoString(pattern))
}
