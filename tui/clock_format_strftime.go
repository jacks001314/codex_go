package tui

import "strings"

// parseStrftimeClockPattern mirrors Rust's `parse_strftime`: it classifies a
// glibc `T_FMT` pattern by its hour conversion. Flags, width, and E/O modifiers
// are skipped; unknown conversions (including recursive `%X`/`%c`) make the
// preference unknown rather than guessing.
func parseStrftimeClockPattern(pattern string) (ClockFormat, bool) {
	twelve := false
	twentyFour := false
	for index := 0; index < len(pattern); index++ {
		if pattern[index] != '%' {
			continue
		}
		index++
		for index < len(pattern) && (strings.ContainsRune("_-0^#", rune(pattern[index])) ||
			(pattern[index] >= '0' && pattern[index] <= '9')) {
			index++
		}
		if index < len(pattern) && (pattern[index] == 'E' || pattern[index] == 'O') {
			index++
		}
		if index >= len(pattern) {
			return ClockFormatTwelveHour, false
		}
		switch pattern[index] {
		case 'I', 'l', 'r':
			twelve = true
		case 'H', 'k', 'R', 'T':
			twentyFour = true
		case '%', 'M', 'S', 'p', 'P', 'Z', 'z', 'n', 't':
			// These do not choose an hour representation.
		default:
			return ClockFormatTwelveHour, false
		}
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
