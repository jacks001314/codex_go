package tui

import (
	"strings"
	"sync"
	"time"
)

// ClockFormat mirrors Rust's TUI clock preference (#47321). Unknown host
// preferences preserve twelve-hour output.
type ClockFormat int

const (
	ClockFormatTwelveHour ClockFormat = iota
	ClockFormatTwentyFourHour
)

var (
	systemClockFormatOnce sync.Once
	systemClockFormat     ClockFormat
)

// SystemClockFormat detects the host's short-time preference once.
func SystemClockFormat() ClockFormat {
	systemClockFormatOnce.Do(func() {
		systemClockFormat = ClockFormatTwelveHour
		if detected, ok := detectSystemClockFormat(); ok {
			systemClockFormat = detected
		}
	})
	return systemClockFormat
}

func (f ClockFormat) TimeLayout() string {
	if f == ClockFormatTwentyFourHour {
		return "15:04"
	}
	return "3:04 PM"
}

func (f ClockFormat) DateTimeLayout() string {
	if f == ClockFormatTwentyFourHour {
		return "Jan 2 at 15:04"
	}
	return "Jan 2 at 3:04 PM"
}

func (f ClockFormat) DateTimeYearLayout() string {
	if f == ClockFormatTwentyFourHour {
		return "Jan 2, 2006 at 15:04"
	}
	return "Jan 2, 2006 at 3:04 PM"
}

func (f ClockFormat) FormatTime(value time.Time) string {
	return value.Local().Format(f.TimeLayout())
}

func (f ClockFormat) FormatDateTime(value time.Time) string {
	return value.Local().Format(f.DateTimeLayout())
}

// parseNativeClockPattern mirrors Rust's `parse_native_pattern`: it reads the
// preferred first alternative of a native hour pattern, ignoring quoted
// literals and only interpreting the hour symbols the caller's platform uses.
func parseNativeClockPattern(pattern string, hourSymbols string) (ClockFormat, bool) {
	quoted := false
	twelve := false
	twentyFour := false
	for _, ch := range pattern {
		if ch == '\'' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		if ch == ';' {
			// Windows lists the preferred pattern first.
			break
		}
		if !strings.ContainsRune(hourSymbols, ch) {
			continue
		}
		switch ch {
		case 'h', 'K':
			twelve = true
		case 'H', 'k':
			twentyFour = true
		default:
			return ClockFormatTwelveHour, false
		}
	}
	if quoted {
		return ClockFormatTwelveHour, false
	}
	switch {
	case twelve && !twentyFour:
		return ClockFormatTwelveHour, true
	case twentyFour && !twelve:
		return ClockFormatTwentyFourHour, true
	default:
		return ClockFormatTwelveHour, false
	}
}
