//go:build windows

package windowssandbox

import (
	"bytes"
	"testing"
)

func TestDPAPIRoundTrip(t *testing.T) {
	plain := []byte("codex windows sandbox dpapi round trip")
	protected, err := DPAPIProtect(plain)
	if err != nil {
		t.Fatalf("DPAPIProtect() error = %v", err)
	}
	if len(protected) == 0 {
		t.Fatalf("DPAPIProtect() returned empty ciphertext")
	}
	got, err := DPAPIUnprotect(protected)
	if err != nil {
		t.Fatalf("DPAPIUnprotect() error = %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("DPAPIUnprotect() = %q, want %q", got, plain)
	}
}
