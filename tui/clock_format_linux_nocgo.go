//go:build linux && !cgo

package tui

import "os"

// detectSystemClockFormat falls back to the locale environment when the glibc
// locale database cannot be queried without cgo.
func detectSystemClockFormat() (ClockFormat, bool) {
	locale := pickTimeLocale(os.Getenv)
	if locale == "" {
		return ClockFormatTwelveHour, false
	}
	return fallbackTimeLocale(locale)
}
