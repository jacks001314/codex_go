//go:build windows

package sessionlock

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wtsCurrentSession     = 0xFFFFFFFF
	wtsSessionInfoEx      = 25
	wtsSessionStateLock   = 0
	wtsSessionStateUnlock = 1
	// wtsInfoExLevel1Size covers Level plus the level-1 payload up to
	// SessionFlags: SessionId, SessionState and SessionFlags.
	wtsInfoExLevel1HeaderSize = 4 + 4 + 4 + 4
)

var (
	wtsapi32                        = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory               = wtsapi32.NewProc("WTSFreeMemory")
)

// SystemDetector returns the Windows session lock detector.
func SystemDetector() Detector { return windowsDetector{} }

type windowsDetector struct{}

func (windowsDetector) Locked() (bool, error) {
	var buffer *byte
	var bytes uint32
	result, _, callErr := procWTSQuerySessionInformationW.Call(
		uintptr(0), // WTS_CURRENT_SERVER_HANDLE
		uintptr(wtsCurrentSession),
		uintptr(wtsSessionInfoEx),
		uintptr(unsafe.Pointer(&buffer)),
		uintptr(unsafe.Pointer(&bytes)),
	)
	if result == 0 {
		if callErr == nil || callErr == windows.ERROR_SUCCESS {
			callErr = errors.New("WTSQuerySessionInformationW failed")
		}
		return false, callErr
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buffer)))
	if buffer == nil {
		return false, errors.New("invalid Windows session information buffer")
	}
	data := unsafe.Slice(buffer, int(bytes))
	return ParseWTSInfoEx(data)
}

// ParseWTSInfoEx reads the lock state from a WTSSessionInfoEx buffer
// (Rust #59330 windows::is_locked).
func ParseWTSInfoEx(data []byte) (bool, error) {
	if len(data) < wtsInfoExLevel1HeaderSize {
		return false, errors.New("invalid Windows session information buffer")
	}
	level := binary.LittleEndian.Uint32(data[0:4])
	if level != 1 {
		return false, fmt.Errorf("unsupported Windows session information level: %d", level)
	}
	flags := int32(binary.LittleEndian.Uint32(data[12:16]))
	switch flags {
	case wtsSessionStateLock:
		return true, nil
	case wtsSessionStateUnlock:
		return false, nil
	default:
		return false, fmt.Errorf("unknown Windows session lock state: %d", flags)
	}
}
