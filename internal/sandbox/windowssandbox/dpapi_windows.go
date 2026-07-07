//go:build windows

package windowssandbox

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

func DPAPIProtect(data []byte) ([]byte, error) {
	in := dataBlobFromBytes(data)
	var out windows.DataBlob
	err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	runtime.KeepAlive(data)
	if err != nil {
		return nil, err
	}
	defer freeDataBlob(out)
	return bytesFromDataBlob(out), nil
}

func DPAPIUnprotect(data []byte) ([]byte, error) {
	in := dataBlobFromBytes(data)
	var out windows.DataBlob
	err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	runtime.KeepAlive(data)
	if err != nil {
		return nil, err
	}
	defer freeDataBlob(out)
	return bytesFromDataBlob(out), nil
}

func dataBlobFromBytes(data []byte) windows.DataBlob {
	if len(data) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{
		Size: uint32(len(data)),
		Data: &data[0],
	}
}

func bytesFromDataBlob(blob windows.DataBlob) []byte {
	if blob.Size == 0 || blob.Data == nil {
		return []byte{}
	}
	data := unsafe.Slice(blob.Data, int(blob.Size))
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func freeDataBlob(blob windows.DataBlob) {
	if blob.Data == nil {
		return
	}
	_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(blob.Data)))
}
