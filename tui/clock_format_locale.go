package tui

import "strings"

// pickTimeLocale mirrors Rust's `time_locale`: LC_ALL wins, then LC_TIME, then
// LANG. Empty values are skipped without trimming so an invalid setting is
// preserved for the platform locale lookup.
func pickTimeLocale(lookup func(string) string) string {
	for _, name := range []string{"LC_ALL", "LC_TIME", "LANG"} {
		if value := lookup(name); value != "" {
			return value
		}
	}
	return ""
}

// standardTimeLocale recognizes the C and POSIX locales, which use a 24-hour
// clock. Rust treats these names as 24-hour without consulting the locale
// database.
func standardTimeLocale(locale string) (ClockFormat, bool) {
	switch locale {
	case "C", "POSIX", "C.UTF-8", "C.utf8":
		return ClockFormatTwentyFourHour, true
	default:
		return ClockFormatTwelveHour, false
	}
}

// fallbackTimeLocale approximates glibc's LC_TIME preference when the locale
// database is unavailable (for example a CGO-disabled build). It keeps the
// common twelve-hour English locales at 12 hours and treats other locales as
// 24-hour, matching glibc for the locales most users select.
func fallbackTimeLocale(locale string) (ClockFormat, bool) {
	if format, ok := standardTimeLocale(locale); ok {
		return format, true
	}
	if !plausibleTimeLocale(locale) {
		// Rust treats locales the host cannot load as an unknown preference, so
		// keep the 12-hour fallback instead of interpreting typo'd names.
		return ClockFormatTwelveHour, false
	}
	lower := strings.ToLower(locale)
	if strings.HasPrefix(lower, "en_us") || strings.HasPrefix(lower, "en_ca") {
		return ClockFormatTwelveHour, true
	}
	return ClockFormatTwentyFourHour, true
}

// plausibleTimeLocale checks the language[_territory][.codeset][@modifier]
// shape without requiring the locale to exist on disk.
func plausibleTimeLocale(locale string) bool {
	end := len(locale)
	if index := strings.IndexAny(locale, ".@"); index >= 0 {
		end = index
	}
	parts := strings.Split(locale[:end], "_")
	if len(parts[0]) < 2 || len(parts[0]) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, ch := range part {
			if !isASCIIAlnum(ch) {
				return false
			}
		}
	}
	for _, ch := range locale[end:] {
		if ch != '.' && ch != '@' && ch != '-' && ch != '_' && !isASCIIAlnum(ch) {
			return false
		}
	}
	return true
}

func isASCIIAlnum(ch rune) bool {
	return ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}
