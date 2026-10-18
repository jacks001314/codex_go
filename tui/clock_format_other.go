//go:build !windows && !linux

package tui

import "os"

func detectSystemClockFormat() (ClockFormat, bool) {
	locale := pickTimeLocale(os.Getenv)
	if locale == "" {
		return ClockFormatTwelveHour, false
	}
	return fallbackTimeLocale(locale)
}
