//go:build windows

package tui

import (
	"encoding/binary"
	"testing"
)

func windowsTestKeyRecord(character uint16, keyDown bool, eventType uint16) []byte {
	record := make([]byte, windowsInputRecordSize)
	binary.LittleEndian.PutUint16(record[0:2], eventType)
	if keyDown {
		binary.LittleEndian.PutUint32(record[4:8], 1)
	}
	binary.LittleEndian.PutUint16(record[14:16], character)
	return record
}

// TestWindowsKeyRecordByte covers the console record extraction: key-down ASCII
// events yield their byte, while key-up, non-key, and non-ASCII records are
// ignored so the probe never mistakes them for a reply.
func TestWindowsKeyRecordByte(t *testing.T) {
	if got, ok := windowsKeyRecordByte(windowsTestKeyRecord('A', true, windowsKeyEventType)); !ok || got != 'A' {
		t.Fatalf("key down = (%q, %v)", got, ok)
	}
	if _, ok := windowsKeyRecordByte(windowsTestKeyRecord('A', false, windowsKeyEventType)); ok {
		t.Fatal("key up must be ignored")
	}
	if _, ok := windowsKeyRecordByte(windowsTestKeyRecord('A', true, 0x0002)); ok {
		t.Fatal("non-key events must be ignored")
	}
	if _, ok := windowsKeyRecordByte(windowsTestKeyRecord(0x4e2d, true, windowsKeyEventType)); ok {
		t.Fatal("non-ASCII input must be ignored")
	}
	if _, ok := windowsKeyRecordByte(nil); ok {
		t.Fatal("short records must be ignored")
	}
}

// TestWindowsReplayOmitsOnlyResponseRecords covers the replay filter: records
// that formed a probe reply are dropped while the rest are preserved.
func TestWindowsReplayOmitsOnlyResponseRecords(t *testing.T) {
	// Byte 0 ('\x1b') and byte 1 (']') belong to the reply; byte 2 ('x') is
	// unrelated typeahead.
	bytesRead := []byte{0x1b, ']', 'x'}
	records := make([]byte, 0, 3*windowsInputRecordSize)
	records = append(records, windowsTestKeyRecord(0x1b, true, windowsKeyEventType)...)
	records = append(records, windowsTestKeyRecord(']', true, windowsKeyEventType)...)
	records = append(records, windowsTestKeyRecord('x', true, windowsKeyEventType)...)
	recordBytes := []int{0, 1, 2}

	ranges := terminalColorResponseRanges(bytesRead)
	omitted := map[int]bool{}
	for _, span := range ranges {
		for byteIndex := span[0]; byteIndex < span[1]; byteIndex++ {
			omitted[recordBytes[byteIndex]] = true
		}
	}
	if omitted[2] {
		t.Fatal("unrelated typeahead must be replayed")
	}

	// A complete reply maps back to its records only.
	reply := []byte("\x1b]10;rgb:ffff/ffff/ffff\x07\x1b]11;rgb:0000/0000/0000\x07")
	replyRecords := make([]byte, 0, len(reply)*windowsInputRecordSize)
	replyBytes := make([]int, 0, len(reply))
	for index, character := range reply {
		replyRecords = append(replyRecords, windowsTestKeyRecord(uint16(character), true, windowsKeyEventType)...)
		replyBytes = append(replyBytes, index)
	}
	omitted = map[int]bool{}
	for _, span := range terminalColorResponseRanges(reply) {
		for byteIndex := span[0]; byteIndex < span[1]; byteIndex++ {
			omitted[replyBytes[byteIndex]] = true
		}
	}
	for index := range reply {
		if !omitted[index] {
			t.Fatalf("reply record %d was not omitted", index)
		}
	}
}

// TestConsoleWindowClassRuleLikeRust mirrors Rust #46504: the native console
// palette is only a valid fallback when the console window is the native
// ConsoleWindowClass, never for ConPTY's PseudoConsoleWindow.
func TestConsoleWindowClassRuleLikeRust(t *testing.T) {
	original := consoleWindowClassName
	t.Cleanup(func() { consoleWindowClassName = original })
	for className, want := range map[string]bool{
		"ConsoleWindowClass":  true,
		"PseudoConsoleWindow": false,
		"":                    false,
		"consolewindowclass":  false,
	} {
		name := className
		consoleWindowClassName = func() string { return name }
		if got := consoleWindowClassIsNative(); got != want {
			t.Fatalf("consoleWindowClassIsNative(%q) = %v, want %v", className, got, want)
		}
	}
}

// TestNativePaletteFallbackHonorsConsoleClassLikeRust pins that the platform
// palette source skips the console color table for a non-native console window
// and still reads it for ConsoleWindowClass.
func TestNativePaletteFallbackHonorsConsoleClassLikeRust(t *testing.T) {
	original := consoleWindowClassName
	originalQuery := queryConsoleDefaultColors
	t.Cleanup(func() {
		consoleWindowClassName = original
		queryConsoleDefaultColors = originalQuery
	})
	queryConsoleDefaultColors = func() (DefaultColors, bool) {
		return DefaultColors{FG: RGBColor{R: 1, G: 2, B: 3}, BG: RGBColor{R: 4, G: 5, B: 6}}, true
	}

	consoleWindowClassName = func() string { return "PseudoConsoleWindow" }
	if _, ok := platformTerminalDefaultColors(); ok {
		t.Fatal("a ConPTY console window must not supply the native palette")
	}
	consoleWindowClassName = func() string { return "ConsoleWindowClass" }
	if colors, ok := platformTerminalDefaultColors(); !ok || colors.BG != (RGBColor{R: 4, G: 5, B: 6}) {
		t.Fatalf("native console window palette = %#v, %v; want the queried colors", colors, ok)
	}
}

// TestConsoleWindowClassNameQueryable checks the Win32 seam reads a class name
// (or reports no console) without panicking.
func TestConsoleWindowClassNameQueryable(t *testing.T) {
	t.Logf("console window class = %q", queryConsoleWindowClassName())
}
