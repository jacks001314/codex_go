//go:build !windows

package mxc

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunMainRequiresWindowsLikeRust(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunMain(strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("RunMain() = %d, want 1", code)
	}
	if got, want := stderr.String(), UnsupportedPlatformMessage+"\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
