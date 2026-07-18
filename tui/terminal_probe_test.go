package tui

import "testing"

func TestParseCursorPositionMatchesRust(t *testing.T) {
	position, ok := ParseCursorPosition([]byte("\x1b[20;10R"))
	if !ok || position != (TerminalPosition{X: 9, Y: 19}) {
		t.Fatalf("position = %#v, %v", position, ok)
	}

	position, ok = ParseCursorPosition([]byte("\x1b[I\x1b[20;10R"))
	if !ok || position != (TerminalPosition{X: 9, Y: 19}) {
		t.Fatalf("position with focus = %#v, %v", position, ok)
	}

	position, ok = ParseCursorPosition([]byte("\x1b[0;0R"))
	if !ok || position != (TerminalPosition{X: 0, Y: 0}) {
		t.Fatalf("saturating position = %#v, %v", position, ok)
	}
}

func TestParseOSCColorsMatchesRust(t *testing.T) {
	color, ok := ParseOSCColor([]byte("\x1b]10;rgb:ffff/8000/0000\x07"), 10)
	if !ok || color != (RGBColor{R: 255, G: 127, B: 0}) {
		t.Fatalf("bel color = %#v, %v", color, ok)
	}

	color, ok = ParseOSCColor([]byte("\x1b]11;rgba:00/80/ff/ff\x1b\\"), 11)
	if !ok || color != (RGBColor{R: 0, G: 128, B: 255}) {
		t.Fatalf("st color = %#v, %v", color, ok)
	}
}

func TestParseOSCRGBComponentsMatchRust(t *testing.T) {
	color, ok := ParseOSCRGB("rgb:00/80/ff")
	if !ok || color != (RGBColor{R: 0, G: 128, B: 255}) {
		t.Fatalf("2-digit color = %#v, %v", color, ok)
	}

	color, ok = ParseOSCRGB("rgba:ffff/8000/0000/ffff")
	if !ok || color != (RGBColor{R: 255, G: 127, B: 0}) {
		t.Fatalf("4-digit color = %#v, %v", color, ok)
	}
}

func TestParseDefaultColorsFromOneBufferMatchesRust(t *testing.T) {
	colors, ok := ParseDefaultColors([]byte("\x1b]10;rgb:eeee/eeee/eeee\x1b\\\x1b]11;rgb:1111/1111/1111\x07"))
	if !ok || colors != (DefaultColors{FG: RGBColor{R: 238, G: 238, B: 238}, BG: RGBColor{R: 17, G: 17, B: 17}}) {
		t.Fatalf("colors = %#v, %v", colors, ok)
	}

	colors, ok = ParseDefaultColors([]byte("\x1b]11;rgb:1111/1111/1111\x07\x1b]10;rgb:eeee/eeee/eeee\x1b\\"))
	if !ok || colors != (DefaultColors{FG: RGBColor{R: 238, G: 238, B: 238}, BG: RGBColor{R: 17, G: 17, B: 17}}) {
		t.Fatalf("colors reversed = %#v, %v", colors, ok)
	}

	if _, ok := ParseDefaultColors([]byte("\x1b]10;rgb:eeee/eeee/eeee\x1b\\")); ok {
		t.Fatal("partial colors should not parse")
	}
}

func TestParseDefaultColorsIgnoresMalformedResponses(t *testing.T) {
	cases := [][]byte{
		[]byte("\x1b]10;rgb:eeee/eeee/eeee\x1b\\\x1b]11;rgb:nope\x07"),
		[]byte("\x1b]10;rgb:eeee/eeee/eeee\x1b\\\x1b]11;rgb:11/11/11/11\x07"),
		[]byte("\x1b]10;rgb:eeee/eeee/eeee\x1b\\\x1b]11;rgb:1111/1111/1111"),
	}
	for _, input := range cases {
		if colors, ok := ParseDefaultColors(input); ok {
			t.Fatalf("malformed colors parsed: %#v from %q", colors, input)
		}
	}
}

func TestParseKeyboardEnhancementSupportMatchesRust(t *testing.T) {
	if state := ParseKeyboardEnhancementSupport([]byte("\x1b[?7u")); state != KeyboardProbeSupported {
		t.Fatalf("flags state = %v", state)
	}
	if state := ParseKeyboardEnhancementSupport([]byte("\x1b[?64;1;2c")); state != KeyboardProbeUnsupportedFallback {
		t.Fatalf("fallback state = %v", state)
	}
	if state := ParseKeyboardEnhancementSupport([]byte("\x1b[?64;1;2c\x1b[?7u")); state != KeyboardProbeSupportedAndFallback {
		t.Fatalf("supported+fallback state = %v", state)
	}
	if state := ParseKeyboardEnhancementSupport([]byte("")); state != KeyboardProbePending {
		t.Fatalf("pending state = %v", state)
	}

	flags, ok := FindKeyboardFlags([]byte("\x1b[?7u"))
	want := KeyboardEnhancementDisambiguateEscapeCodes | KeyboardEnhancementReportEventTypes | KeyboardEnhancementReportAlternateKeys
	if !ok || flags != want {
		t.Fatalf("flags = %v, %v", flags, ok)
	}
}

func TestStartupProbeParsesBatchedTerminalResponses(t *testing.T) {
	probe := StartupProbe{}
	sawSupportedKeyboard := false
	UpdateStartupProbe(
		&probe,
		&sawSupportedKeyboard,
		[]byte("\x1b[20;10R\x1b]11;rgb:1111/1111/1111\x07\x1b[?64;1;2c\x1b]10;rgb:eeee/eeee/eeee\x1b\\\x1b[?7u"),
		StartupKeyboardEnhancementProbeQuery,
	)

	if probe.CursorPosition == nil || *probe.CursorPosition != (TerminalPosition{X: 9, Y: 19}) {
		t.Fatalf("cursor = %#v", probe.CursorPosition)
	}
	if probe.DefaultColors == nil || *probe.DefaultColors != (DefaultColors{FG: RGBColor{R: 238, G: 238, B: 238}, BG: RGBColor{R: 17, G: 17, B: 17}}) {
		t.Fatalf("colors = %#v", probe.DefaultColors)
	}
	if probe.KeyboardEnhancementSupported == nil || !*probe.KeyboardEnhancementSupported {
		t.Fatalf("keyboard support = %#v", probe.KeyboardEnhancementSupported)
	}
	if !StartupProbeComplete(probe, StartupKeyboardEnhancementProbeQuery) {
		t.Fatal("startup probe should be complete")
	}
}

func TestFinishStartupProbePromotesDeferredKeyboardSupport(t *testing.T) {
	probe := StartupProbe{}
	FinishStartupProbe(&probe, StartupKeyboardEnhancementProbeQuery, true)
	if probe.KeyboardEnhancementSupported == nil || !*probe.KeyboardEnhancementSupported {
		t.Fatalf("finish support = %#v", probe.KeyboardEnhancementSupported)
	}

	probe = StartupProbe{}
	FinishStartupProbe(&probe, StartupKeyboardEnhancementProbeSkip, true)
	if probe.KeyboardEnhancementSupported != nil {
		t.Fatalf("skip should not set support: %#v", probe.KeyboardEnhancementSupported)
	}
}
