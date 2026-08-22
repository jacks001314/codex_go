package tui

import (
	"runtime"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestChromaFormatterForLevelMatchesTerminalDepth(t *testing.T) {
	cases := []struct {
		level StdoutColorLevel
		want  string
	}{
		{ColorTrue, "terminal16m"},
		{ColorANSI256, "terminal256"},
		{ColorANSI16, "terminal16"},
		{ColorUnknown, "terminal16m"},
	}
	for _, tc := range cases {
		if got := ChromaFormatterForLevel(tc.level); got != tc.want {
			t.Errorf("ChromaFormatterForLevel(%v) = %q, want %q", tc.level, got, tc.want)
		}
	}
}

func TestTermenvProfileForLevelMatchesTerminalDepth(t *testing.T) {
	if got := TermenvProfileForLevel(ColorTrue); got != termenv.TrueColor {
		t.Errorf("TermenvProfileForLevel(ColorTrue) = %v, want TrueColor", got)
	}
	if got := TermenvProfileForLevel(ColorANSI256); got != termenv.ANSI256 {
		t.Errorf("TermenvProfileForLevel(ColorANSI256) = %v, want ANSI256", got)
	}
	if got := TermenvProfileForLevel(ColorANSI16); got != termenv.ANSI {
		t.Errorf("TermenvProfileForLevel(ColorANSI16) = %v, want ANSI", got)
	}
}

func TestDiffBgSGRAdaptsToColorDepth(t *testing.T) {
	// ANSI-16 terminals must never receive a tinted line background.
	if got := diffBgSGR(DiffLineInsert, false, ColorANSI16); got != "" {
		t.Fatalf("ANSI-16 insert bg = %q, want empty", got)
	}
	if got := diffBgSGR(DiffLineDelete, false, ColorANSI16); got != "" {
		t.Fatalf("ANSI-16 delete bg = %q, want empty", got)
	}
	// Truecolor keeps the RGB palette; 256-color quantizes to indices.
	if got := diffBgSGR(DiffLineInsert, false, ColorTrue); got != ansiBgAddDark {
		t.Fatalf("truecolor insert bg = %q, want %q", got, ansiBgAddDark)
	}
	if got := diffBgSGR(DiffLineInsert, false, ColorANSI256); got != "\x1b[48;5;22m" {
		t.Fatalf("256 insert bg = %q, want \\x1b[48;5;22m", got)
	}
	if got := diffBgSGR(DiffLineDelete, false, ColorANSI256); got != "\x1b[48;5;52m" {
		t.Fatalf("256 delete bg = %q, want \\x1b[48;5;52m", got)
	}
}

func TestDiffSignSGRUsesForegroundOnlyForAnsi16(t *testing.T) {
	// On ANSI-16 the sign must not carry a tinted background.
	for _, light := range []bool{true, false} {
		for _, lineType := range []DiffLineType{DiffLineInsert, DiffLineDelete} {
			got := diffSignSGR(lineType, light, ColorANSI16)
			if strings.Contains(got, "48;") {
				t.Fatalf("sign SGR for ANSI-16 = %q, must not contain a background", got)
			}
		}
	}
	// Dark truecolor combines sign fg with the line background, like Rust.
	got := diffSignSGR(DiffLineInsert, false, ColorTrue)
	if !strings.Contains(got, "32") || !strings.Contains(got, "48;2;33;58;43") {
		t.Fatalf("dark truecolor insert sign = %q, want green fg + add bg", got)
	}
}

func TestDetectStdoutColorLevelPromotesWindowsTerminal(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Terminal promotion only applies on Windows")
	}
	t.Setenv("WT_SESSION", "1")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("FORCE_COLOR", "")
	if got := detectStdoutColorLevel(); got != ColorTrue {
		t.Fatalf("WT_SESSION promotion = %v, want ColorTrue", got)
	}
	t.Setenv("WT_SESSION", "")
	t.Setenv("TERM_PROGRAM", "Windows_Terminal")
	if got := detectStdoutColorLevel(); got != ColorTrue {
		t.Fatalf("TERM_PROGRAM promotion = %v, want ColorTrue", got)
	}
}
