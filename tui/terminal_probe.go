package tui

import (
	"strconv"
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/terminal_probe.rs.
const DefaultTerminalProbeTimeout = 100 * time.Millisecond

type TerminalProbeResult struct {
	Terminal TerminalName
	VSCode   bool
}

type DefaultColors struct {
	FG RGBColor
	BG RGBColor
}

type RGBColor struct {
	R uint8
	G uint8
	B uint8
}

type TerminalPosition struct {
	X uint16
	Y uint16
}

type StartupProbe struct {
	CursorPosition               *TerminalPosition
	DefaultColors                *DefaultColors
	KeyboardEnhancementSupported *bool
}

type StartupKeyboardEnhancementProbe int

const (
	StartupKeyboardEnhancementProbeQuery StartupKeyboardEnhancementProbe = iota
	StartupKeyboardEnhancementProbeSkip
)

type KeyboardEnhancementFlags uint8

const (
	KeyboardEnhancementDisambiguateEscapeCodes KeyboardEnhancementFlags = 1 << iota
	KeyboardEnhancementReportEventTypes
	KeyboardEnhancementReportAlternateKeys
	KeyboardEnhancementReportAllKeysAsEscapeCodes
)

type KeyboardProbeState int

const (
	KeyboardProbePending KeyboardProbeState = iota
	KeyboardProbeUnsupportedFallback
	KeyboardProbeSupported
	KeyboardProbeSupportedAndFallback
)

func ParseCursorPosition(buffer []byte) (TerminalPosition, bool) {
	for _, start := range findAllSubslice(buffer, []byte("\x1b[")) {
		rest := buffer[start+2:]
		end := byteIndex(rest, 'R')
		if end < 0 {
			continue
		}
		payload := string(rest[:end])
		parts := strings.Split(payload, ";")
		if len(parts) != 2 {
			continue
		}
		row, ok := parseUint16Text(parts[0])
		if !ok {
			continue
		}
		col, ok := parseUint16Text(parts[1])
		if !ok {
			continue
		}
		return TerminalPosition{X: saturatingSubOne(col), Y: saturatingSubOne(row)}, true
	}
	return TerminalPosition{}, false
}

func ParseOSCColor(buffer []byte, slot byte) (RGBColor, bool) {
	prefix := []byte("\x1b]" + strconv.Itoa(int(slot)) + ";")
	start := findSubslice(buffer, prefix)
	if start < 0 {
		return RGBColor{}, false
	}
	rest := buffer[start+len(prefix):]
	payloadEnd, ok := oscPayloadEnd(rest)
	if !ok {
		return RGBColor{}, false
	}
	return ParseOSCRGB(string(rest[:payloadEnd]))
}

func ParseDefaultColors(buffer []byte) (DefaultColors, bool) {
	fg, ok := ParseOSCColor(buffer, 10)
	if !ok {
		return DefaultColors{}, false
	}
	bg, ok := ParseOSCColor(buffer, 11)
	if !ok {
		return DefaultColors{}, false
	}
	return DefaultColors{FG: fg, BG: bg}, true
}

func ParseOSCRGB(payload string) (RGBColor, bool) {
	prefix, values, ok := strings.Cut(strings.TrimSpace(payload), ":")
	if !ok {
		return RGBColor{}, false
	}
	if !strings.EqualFold(prefix, "rgb") && !strings.EqualFold(prefix, "rgba") {
		return RGBColor{}, false
	}

	parts := strings.Split(values, "/")
	required := 3
	if strings.EqualFold(prefix, "rgba") {
		required = 4
	}
	if len(parts) != required {
		return RGBColor{}, false
	}

	r, ok := parseOSCComponent(parts[0])
	if !ok {
		return RGBColor{}, false
	}
	g, ok := parseOSCComponent(parts[1])
	if !ok {
		return RGBColor{}, false
	}
	b, ok := parseOSCComponent(parts[2])
	if !ok {
		return RGBColor{}, false
	}
	if required == 4 {
		if _, ok := parseOSCComponent(parts[3]); !ok {
			return RGBColor{}, false
		}
	}
	return RGBColor{R: r, G: g, B: b}, true
}

func ParseKeyboardEnhancementSupport(buffer []byte) KeyboardProbeState {
	_, hasFlags := FindKeyboardFlags(buffer)
	hasFallback := FindPrimaryDeviceAttributes(buffer)
	switch {
	case hasFlags && hasFallback:
		return KeyboardProbeSupportedAndFallback
	case hasFlags:
		return KeyboardProbeSupported
	case hasFallback:
		return KeyboardProbeUnsupportedFallback
	default:
		return KeyboardProbePending
	}
}

func FindKeyboardFlags(buffer []byte) (KeyboardEnhancementFlags, bool) {
	for _, start := range findAllSubslice(buffer, []byte("\x1b[?")) {
		rest := buffer[start+3:]
		end := byteIndex(rest, 'u')
		if end <= 0 {
			continue
		}
		bits, err := strconv.ParseUint(string(rest[:end]), 10, 8)
		if err != nil {
			continue
		}
		var flags KeyboardEnhancementFlags
		if bits&1 != 0 {
			flags |= KeyboardEnhancementDisambiguateEscapeCodes
		}
		if bits&2 != 0 {
			flags |= KeyboardEnhancementReportEventTypes
		}
		if bits&4 != 0 {
			flags |= KeyboardEnhancementReportAlternateKeys
		}
		if bits&8 != 0 {
			flags |= KeyboardEnhancementReportAllKeysAsEscapeCodes
		}
		return flags, true
	}
	return 0, false
}

func FindPrimaryDeviceAttributes(buffer []byte) bool {
	for _, start := range findAllSubslice(buffer, []byte("\x1b[?")) {
		rest := buffer[start+3:]
		end := byteIndex(rest, 'c')
		if end <= 0 {
			continue
		}
		if asciiDigitsAndSemicolons(rest[:end]) {
			return true
		}
	}
	return false
}

func UpdateStartupProbe(probe *StartupProbe, sawSupportedKeyboard *bool, buffer []byte, keyboardProbe StartupKeyboardEnhancementProbe) {
	if probe == nil {
		return
	}
	if probe.CursorPosition == nil {
		if position, ok := ParseCursorPosition(buffer); ok {
			probe.CursorPosition = &position
		}
	}
	if probe.DefaultColors == nil {
		if colors, ok := ParseDefaultColors(buffer); ok {
			probe.DefaultColors = &colors
		}
	}
	if keyboardProbe == StartupKeyboardEnhancementProbeSkip || probe.KeyboardEnhancementSupported != nil {
		return
	}
	switch ParseKeyboardEnhancementSupport(buffer) {
	case KeyboardProbeSupportedAndFallback:
		probe.KeyboardEnhancementSupported = boolPtrTerminalProbe(true)
	case KeyboardProbeSupported:
		if sawSupportedKeyboard != nil {
			*sawSupportedKeyboard = true
		}
	case KeyboardProbeUnsupportedFallback:
		probe.KeyboardEnhancementSupported = boolPtrTerminalProbe(false)
	}
}

func StartupProbeComplete(probe StartupProbe, keyboardProbe StartupKeyboardEnhancementProbe) bool {
	return probe.CursorPosition != nil &&
		probe.DefaultColors != nil &&
		(keyboardProbe == StartupKeyboardEnhancementProbeSkip || probe.KeyboardEnhancementSupported != nil)
}

func FinishStartupProbe(probe *StartupProbe, keyboardProbe StartupKeyboardEnhancementProbe, sawSupportedKeyboard bool) {
	if probe == nil {
		return
	}
	if keyboardProbe == StartupKeyboardEnhancementProbeQuery && probe.KeyboardEnhancementSupported == nil && sawSupportedKeyboard {
		probe.KeyboardEnhancementSupported = boolPtrTerminalProbe(true)
	}
}

func parseOSCComponent(component string) (uint8, bool) {
	switch len(component) {
	case 2:
		value, err := strconv.ParseUint(component, 16, 8)
		return uint8(value), err == nil
	case 4:
		value, err := strconv.ParseUint(component, 16, 16)
		if err != nil {
			return 0, false
		}
		return uint8(value / 257), true
	default:
		return 0, false
	}
}

func oscPayloadEnd(buffer []byte) (int, bool) {
	for idx := 0; idx < len(buffer); idx++ {
		switch buffer[idx] {
		case 0x07:
			return idx, true
		case 0x1b:
			if idx+1 < len(buffer) && buffer[idx+1] == '\\' {
				return idx, true
			}
		}
	}
	return 0, false
}

func parseUint16Text(text string) (uint16, bool) {
	value, err := strconv.ParseUint(text, 10, 16)
	if err != nil {
		return 0, false
	}
	return uint16(value), true
}

func saturatingSubOne(value uint16) uint16 {
	if value == 0 {
		return 0
	}
	return value - 1
}

func findSubslice(haystack []byte, needle []byte) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for idx := 0; idx <= len(haystack)-len(needle); idx++ {
		if string(haystack[idx:idx+len(needle)]) == string(needle) {
			return idx
		}
	}
	return -1
}

func findAllSubslice(haystack []byte, needle []byte) []int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return nil
	}
	matches := []int{}
	for idx := 0; idx <= len(haystack)-len(needle); idx++ {
		if string(haystack[idx:idx+len(needle)]) == string(needle) {
			matches = append(matches, idx)
		}
	}
	return matches
}

func byteIndex(buffer []byte, target byte) int {
	for idx, value := range buffer {
		if value == target {
			return idx
		}
	}
	return -1
}

func asciiDigitsAndSemicolons(buffer []byte) bool {
	for _, value := range buffer {
		if (value < '0' || value > '9') && value != ';' {
			return false
		}
	}
	return true
}

func boolPtrTerminalProbe(value bool) *bool {
	return &value
}
