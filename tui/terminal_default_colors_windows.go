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

// probePlatformTerminalDefaultColors reports no OSC probe on Windows: the
// console color table above is the platform source (Rust prefers a terminal OSC
// response there too, which needs the Windows console input-replay path Go does
// not port).
func probePlatformTerminalDefaultColors(timeout time.Duration) (DefaultColors, bool) {
	return DefaultColors{}, false
}
