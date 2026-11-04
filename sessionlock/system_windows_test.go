//go:build windows

package sessionlock

import (
	"encoding/binary"
	"testing"
)

func wtsInfoExBuffer(level uint32, flags int32) []byte {
	buffer := make([]byte, 48)
	binary.LittleEndian.PutUint32(buffer[0:4], level)
	binary.LittleEndian.PutUint32(buffer[12:16], uint32(flags))
	return buffer
}

func TestParseWTSInfoExReadsSessionFlags(t *testing.T) {
	if locked, err := ParseWTSInfoEx(wtsInfoExBuffer(1, wtsSessionStateLock)); err != nil || !locked {
		t.Fatalf("locked state = %v,%v", locked, err)
	}
	if locked, err := ParseWTSInfoEx(wtsInfoExBuffer(1, wtsSessionStateUnlock)); err != nil || locked {
		t.Fatalf("unlocked state = %v,%v", locked, err)
	}
	if _, err := ParseWTSInfoEx(wtsInfoExBuffer(2, wtsSessionStateLock)); err == nil {
		t.Fatal("unsupported level was accepted")
	}
	if _, err := ParseWTSInfoEx(wtsInfoExBuffer(1, 7)); err == nil {
		t.Fatal("unknown lock state was accepted")
	}
	if _, err := ParseWTSInfoEx([]byte{1, 0, 0}); err == nil {
		t.Fatal("short buffer was accepted")
	}
}
