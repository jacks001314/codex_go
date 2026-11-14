//go:build windows

package mxc

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

func TestNativeProcessLaunchErrorIncludesWin32Code(t *testing.T) {
	for _, code := range []syscall.Errno{2, 5, 87, 1314} {
		err := nativeProcessLaunchError(code)
		if !errors.Is(err, code) {
			t.Fatalf("lost native cause: %v", err)
		}
		if !strings.Contains(err.Error(), fmt.Sprintf("CreateProcessW, Win32 error %d", uint32(code))) {
			t.Fatalf("missing code: %v", err)
		}
	}
	plain := errors.New("launch unavailable")
	if got := nativeProcessLaunchError(plain).Error(); got != "CreateProcessW(SecurityEnvironment): launch unavailable" {
		t.Fatalf("fallback=%q", got)
	}
}
