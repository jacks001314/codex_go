//go:build windows

package tui

import (
	"encoding/binary"
	"syscall"
	"time"
	"unsafe"
)

// Rust parity: codex-rs/tui/src/terminal_probe/windows.rs (#43921). Rust prefers
// a terminal OSC 10/11 response and falls back to the native console color
// table; Go implements the console-table fallback (the OSC query needs to share
// the input queue with the TUI's reader, which bubbletea owns).
const consoleScreenBufferInfoExSize = 96

// Offsets within CONSOLE_SCREEN_BUFFER_INFOEX.
const (
	consoleScreenBufferInfoExAttributes = 12
	consoleScreenBufferInfoExColorTable = 32
)

var getConsoleScreenBufferInfoEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfoEx")

// platformTerminalDefaultColors reads the Windows console's configured default
// foreground/background colors. It reports false when stdout is not a console
// or the query fails.
func platformTerminalDefaultColors() (DefaultColors, bool) {
	var info [consoleScreenBufferInfoExSize]byte
	binary.LittleEndian.PutUint32(info[0:], consoleScreenBufferInfoExSize)
	ret, _, _ := getConsoleScreenBufferInfoEx.Call(
		uintptr(syscall.Stdout),
		uintptr(unsafe.Pointer(&info[0])),
	)
	if ret == 0 {
		return DefaultColors{}, false
	}
	attributes := binary.LittleEndian.Uint16(info[consoleScreenBufferInfoExAttributes:])
	var colorTable [16]uint32
	for i := range colorTable {
		colorTable[i] = binary.LittleEndian.Uint32(info[consoleScreenBufferInfoExColorTable+4*i:])
	}
	return decodeConsoleDefaultColors(attributes, colorTable), true
}

// probePlatformTerminalDefaultColors queries the terminal renderer's OSC 10/11
// colors through the console input queue while preserving every console record
// that is not part of a reply (Rust terminal_probe/windows.rs). When it reports
// false, the console color table above remains the platform source.
func probePlatformTerminalDefaultColors(timeout time.Duration) (DefaultColors, bool) {
	if timeout <= 0 {
		timeout = terminalColorProbeTimeout
	}
	output, ok := windowsStdHandle(stdOutputHandle)
	if !ok {
		return DefaultColors{}, false
	}
	input, ok := windowsStdHandle(stdInputHandle)
	if !ok {
		return DefaultColors{}, false
	}
	if colors, ok := queryWindowsOSCDefaultColors(input, output, timeout); ok {
		return colors, true
	}
	return DefaultColors{}, false
}

const (
	// Win32 STD_*_HANDLE values as unsigned 32-bit DWORDs.
	stdInputHandle  = uintptr(0xFFFFFFF6) // (DWORD)-10
	stdOutputHandle = uintptr(0xFFFFFFF5) // (DWORD)-11

	windowsKeyEventType     = 0x0001
	windowsInputRecordSize  = 20
	windowsProbeReadRecords = 64
	windowsWaitObject0      = 0x00000000
)

var (
	windowsGetStdHandle              = syscall.NewLazyDLL("kernel32.dll").NewProc("GetStdHandle")
	windowsWriteFile                 = syscall.NewLazyDLL("kernel32.dll").NewProc("WriteFile")
	windowsReadConsoleInputW         = syscall.NewLazyDLL("kernel32.dll").NewProc("ReadConsoleInputW")
	windowsWriteConsoleInputW        = syscall.NewLazyDLL("kernel32.dll").NewProc("WriteConsoleInputW")
	windowsGetNumberOfConsoleInputEv = syscall.NewLazyDLL("kernel32.dll").NewProc("GetNumberOfConsoleInputEvents")
	windowsWaitForSingleObject       = syscall.NewLazyDLL("kernel32.dll").NewProc("WaitForSingleObject")
)

func windowsStdHandle(kind uintptr) (uintptr, bool) {
	handle, _, _ := windowsGetStdHandle.Call(kind)
	if handle == 0 || handle == ^uintptr(0) {
		return 0, false
	}
	return handle, true
}

// queryWindowsOSCDefaultColors writes the OSC queries and reads console input
// records until a valid foreground/background pair arrives or the deadline
// expires. Records that did not form a reply are written back to the input queue
// unchanged.
func queryWindowsOSCDefaultColors(input uintptr, output uintptr, timeout time.Duration) (DefaultColors, bool) {
	query := []byte(terminalColorProbeQuery)
	var written uint32
	ret, _, _ := windowsWriteFile.Call(output, uintptr(unsafe.Pointer(&query[0])), uintptr(len(query)), uintptr(unsafe.Pointer(&written)), 0)
	if ret == 0 {
		return DefaultColors{}, false
	}
	deadline := time.Now().Add(timeout)
	var records []byte
	var recordBytes []int // byte index -> record index
	var bytesRead []byte
	readBuffer := make([]byte, windowsInputRecordSize*windowsProbeReadRecords)
	for time.Now().Before(deadline) {
		remainingMS := int(time.Until(deadline).Milliseconds())
		if remainingMS < 0 {
			remainingMS = 0
		}
		if !windowsInputPending(input, uintptr(remainingMS)) {
			continue
		}
		var count uint32
		ret, _, _ := windowsReadConsoleInputW.Call(input, uintptr(unsafe.Pointer(&readBuffer[0])), windowsProbeReadRecords, uintptr(unsafe.Pointer(&count)))
		if ret == 0 || count == 0 {
			continue
		}
		for index := uint32(0); index < count; index++ {
			record := readBuffer[int(index)*windowsInputRecordSize : int(index+1)*windowsInputRecordSize]
			records = append(records, record...)
			if character, ok := windowsKeyRecordByte(record); ok {
				recordBytes = append(recordBytes, len(records)/windowsInputRecordSize-1)
				bytesRead = append(bytesRead, character)
			}
		}
		if colors, ok := terminalDefaultColorsFromResponses(bytesRead); ok {
			replayWindowsConsoleRecords(input, records, recordBytes, bytesRead)
			return colors, true
		}
	}
	replayWindowsConsoleRecords(input, records, recordBytes, nil)
	return DefaultColors{}, false
}

// windowsInputPending waits for console input events without consuming them.
func windowsInputPending(input uintptr, waitMillis uintptr) bool {
	var pending uint32
	ret, _, _ := windowsGetNumberOfConsoleInputEv.Call(input, uintptr(unsafe.Pointer(&pending)))
	if ret == 0 {
		return false
	}
	if pending > 0 {
		return true
	}
	ret, _, _ = windowsWaitForSingleObject.Call(input, waitMillis)
	return ret == windowsWaitObject0
}

// windowsKeyRecordByte extracts a key-down ASCII byte from one INPUT_RECORD.
func windowsKeyRecordByte(record []byte) (byte, bool) {
	if len(record) < windowsInputRecordSize {
		return 0, false
	}
	if binary.LittleEndian.Uint16(record[0:2]) != windowsKeyEventType {
		return 0, false
	}
	if binary.LittleEndian.Uint32(record[4:8]) == 0 { // bKeyDown
		return 0, false
	}
	character := binary.LittleEndian.Uint16(record[14:16])
	if character == 0 || character > 0x7f {
		return 0, false
	}
	return byte(character), true
}

// replayWindowsConsoleRecords writes back every record that did not form a
// probe reply, preserving modifiers, UTF-16 input, mouse events, and focus
// changes.
func replayWindowsConsoleRecords(input uintptr, records []byte, recordBytes []int, responseBytes []byte) {
	if len(records) == 0 {
		return
	}
	responseRanges := terminalColorResponseRanges(responseBytes)
	omitted := map[int]bool{}
	for _, span := range responseRanges {
		for byteIndex := span[0]; byteIndex < span[1]; byteIndex++ {
			if byteIndex < len(recordBytes) {
				omitted[recordBytes[byteIndex]] = true
			}
		}
	}
	preserved := make([]byte, 0, len(records))
	for index := 0; index < len(records)/windowsInputRecordSize; index++ {
		if omitted[index] {
			continue
		}
		preserved = append(preserved, records[index*windowsInputRecordSize:(index+1)*windowsInputRecordSize]...)
	}
	if len(preserved) == 0 {
		return
	}
	var written uint32
	_, _, _ = windowsWriteConsoleInputW.Call(input, uintptr(unsafe.Pointer(&preserved[0])), uintptr(len(preserved)/windowsInputRecordSize), uintptr(unsafe.Pointer(&written)))
}
