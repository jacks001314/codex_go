//go:build windows

package windowssandbox

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func SIDBytesFromString(value string) ([]byte, error) {
	sid, err := windows.StringToSid(value)
	if err != nil {
		return nil, err
	}
	return copySIDBytes(sid), nil
}

func StringFromSIDBytes(sid []byte) string {
	if len(sid) == 0 {
		return ""
	}
	buf := append([]byte(nil), sid...)
	ptr := (*windows.SID)(unsafe.Pointer(&buf[0]))
	if !ptr.IsValid() {
		return ""
	}
	value := ptr.String()
	runtime.KeepAlive(buf)
	return value
}

func copySIDBytes(sid *windows.SID) []byte {
	if sid == nil || !sid.IsValid() {
		return nil
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(sid)), sid.Len())
	out := make([]byte, len(data))
	copy(out, data)
	return out
}
