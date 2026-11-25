//go:build darwin && cgo

package doctor

import (
	"os"
	"testing"
)

func TestDesktopPlatformProcPIDPathRejectsOverflowLikeRust(t *testing.T) {
	if path, ok := darwinProcessExecutablePath(os.Getpid()); !ok || path == "" {
		t.Fatalf("current process path = %q, %t", path, ok)
	}
	// This value wraps to the current process in C.int without a checked
	// conversion; Rust's i32::try_from rejects it before calling libproc.
	wrappedPID := int((uint64(1) << 32) + uint64(os.Getpid()))
	for _, pid := range []int{0, -1, 1 << 31, wrappedPID} {
		if path, ok := darwinProcessExecutablePath(pid); ok || path != "" {
			t.Errorf("pid %d path = %q, %t, want unavailable", pid, path, ok)
		}
	}
}
