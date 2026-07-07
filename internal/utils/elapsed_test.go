package utils

import (
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                          "0ms",
		250 * time.Millisecond:     "250ms",
		1500 * time.Millisecond:    "1.50s",
		59999 * time.Millisecond:   "60.00s",
		75000 * time.Millisecond:   "1m 15s",
		3600000 * time.Millisecond: "60m 00s",
	}
	for duration, want := range cases {
		if got := FormatDuration(duration); got != want {
			t.Fatalf("FormatDuration(%s) = %q want %q", duration, got, want)
		}
	}
}
