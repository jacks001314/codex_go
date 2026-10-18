package tui

import (
	"testing"
	"time"
)

// Rust #47321: twelve- and twenty-four-hour layouts agree on the same instant.
func TestClockFormatLayoutsLikeRust(t *testing.T) {
	midnight := time.Date(2026, 9, 12, 0, 0, 0, 0, time.Local)
	noon := time.Date(2026, 9, 12, 12, 0, 0, 0, time.Local)
	nextDay := time.Date(2026, 9, 13, 4, 5, 0, 0, time.Local)
	tests := []struct {
		name   string
		format ClockFormat
		value  time.Time
		want   string
	}{
		{name: "12h midnight", format: ClockFormatTwelveHour, value: midnight, want: "12:00 AM"},
		{name: "12h noon", format: ClockFormatTwelveHour, value: noon, want: "12:00 PM"},
		{name: "12h next day", format: ClockFormatTwelveHour, value: nextDay, want: "Sep 13 at 4:05 AM"},
		{name: "24h midnight", format: ClockFormatTwentyFourHour, value: midnight, want: "00:00"},
		{name: "24h noon", format: ClockFormatTwentyFourHour, value: noon, want: "12:00"},
		{name: "24h next day", format: ClockFormatTwentyFourHour, value: nextDay, want: "Sep 13 at 04:05"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			var got string
			if testCase.value.Hour() == 0 || testCase.value.Hour() == 12 {
				got = testCase.format.FormatTime(testCase.value)
			} else {
				got = testCase.format.FormatDateTime(testCase.value)
			}
			if got != testCase.want {
				t.Fatalf("format = %q, want %q", got, testCase.want)
			}
		})
	}
	if got := ClockFormatTwelveHour.FormatDateTime(midnight); got != "Sep 12 at 12:00 AM" {
		t.Fatalf("12h date-time = %q", got)
	}
	if got := ClockFormatTwentyFourHour.FormatDateTime(midnight); got != "Sep 12 at 00:00" {
		t.Fatalf("24h date-time = %q", got)
	}
}

func TestParseNativeClockPatternLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		pattern string
		symbols string
		want    ClockFormat
		ok      bool
	}{
		{pattern: "h:mm tt", symbols: "hH", want: ClockFormatTwelveHour, ok: true},
		{pattern: "HH:mm", symbols: "hH", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "h 'H; o''clock' a", symbols: "hHKk", want: ClockFormatTwelveHour, ok: true},
		{pattern: "'h' HH:mm;h:mm tt", symbols: "hH", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "H:mm;h:mm tt", symbols: "hH", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "'h'H:mm", symbols: "hH", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "h:mm tt;HH:mm", symbols: "hH", want: ClockFormatTwelveHour, ok: true},
		{pattern: "K a", symbols: "hHKk", want: ClockFormatTwelveHour, ok: true},
		{pattern: "kk", symbols: "hHKk", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "K HH:mm", symbols: "hH", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "h 'unterminated", symbols: "hH", ok: false},
		{pattern: "h:mm H:mm", symbols: "hH", ok: false},
		{pattern: "'HH' mm", symbols: "hH", ok: false},
		{pattern: "mm:ss", symbols: "hH", ok: false},
		{pattern: "", symbols: "hH", ok: false},
	} {
		got, ok := parseNativeClockPattern(testCase.pattern, testCase.symbols)
		if ok != testCase.ok || (ok && got != testCase.want) {
			t.Fatalf("parseNativeClockPattern(%q, %q) = %v,%v want %v,%v", testCase.pattern, testCase.symbols, got, ok, testCase.want, testCase.ok)
		}
	}
}

func TestTimeLocalePrecedenceLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]string
		want   string
	}{
		{
			name:   "LC_ALL wins",
			values: map[string]string{"LC_ALL": "C", "LC_TIME": "en_US.UTF-8", "LANG": "de_DE.UTF-8"},
			want:   "C",
		},
		{
			name:   "empty LC_ALL falls through",
			values: map[string]string{"LC_ALL": "", "LC_TIME": "C.UTF-8", "LANG": "en_US.UTF-8"},
			want:   "C.UTF-8",
		},
		{
			name:   "LANG is last",
			values: map[string]string{"LC_TIME": "", "LANG": "C.utf8"},
			want:   "C.utf8",
		},
		{
			name:   "invalid value is preserved",
			values: map[string]string{"LC_ALL": "invalid", "LC_TIME": "C"},
			want:   "invalid",
		},
		{
			name:   "nothing set",
			values: map[string]string{},
			want:   "",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := pickTimeLocale(func(name string) string { return testCase.values[name] })
			if got != testCase.want {
				t.Fatalf("pickTimeLocale = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestTimeLocaleFallbackLikeRust(t *testing.T) {
	for _, locale := range []string{"C", "POSIX", "C.UTF-8", "C.utf8"} {
		got, ok := standardTimeLocale(locale)
		if !ok || got != ClockFormatTwentyFourHour {
			t.Fatalf("standardTimeLocale(%q) = %v,%v want 24h,true", locale, got, ok)
		}
	}
	for _, locale := range []string{"en_US.UTF-8", "en_CA"} {
		got, ok := fallbackTimeLocale(locale)
		if !ok || got != ClockFormatTwelveHour {
			t.Fatalf("fallbackTimeLocale(%q) = %v,%v want 12h,true", locale, got, ok)
		}
	}
	if got, ok := fallbackTimeLocale("de_DE.UTF-8"); !ok || got != ClockFormatTwentyFourHour {
		t.Fatalf("fallbackTimeLocale(de_DE.UTF-8) = %v,%v want 24h,true", got, ok)
	}
	for _, locale := range []string{"codex_missing_locale", "C.codex_missing_encoding", "invalid\x00locale"} {
		if got, ok := fallbackTimeLocale(locale); ok {
			t.Fatalf("fallbackTimeLocale(%q) = %v,true want unknown", locale, got)
		}
	}
}

func TestStrftimeClockPatternLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		pattern string
		want    ClockFormat
		ok      bool
	}{
		{pattern: "%I:%M:%S %p", want: ClockFormatTwelveHour, ok: true},
		{pattern: "%l:%M %P", want: ClockFormatTwelveHour, ok: true},
		{pattern: "%r", want: ClockFormatTwelveHour, ok: true},
		{pattern: "%H:%M:%S", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "%k:%M", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "%R", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "%T %Z", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "%%I %_2OH:%M", want: ClockFormatTwentyFourHour, ok: true},
		{pattern: "%%H %-I:%M", want: ClockFormatTwelveHour, ok: true},
		{pattern: "%H %I", ok: false},
		{pattern: "%H %X", ok: false},
		{pattern: "%H %", ok: false},
		{pattern: "%H %Q", ok: false},
		{pattern: "%p", ok: false},
		{pattern: "%%H", ok: false},
		{pattern: "", ok: false},
	} {
		got, ok := parseStrftimeClockPattern(testCase.pattern)
		if ok != testCase.ok || (ok && got != testCase.want) {
			t.Fatalf("parseStrftimeClockPattern(%q) = %v,%v want %v,%v", testCase.pattern, got, ok, testCase.want, testCase.ok)
		}
	}
}
