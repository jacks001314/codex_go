package keyring

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

// TestWindowsTargetNameAndSecretEncoding pins the keyring crate's Windows
// mapping so credentials written by either implementation are interchangeable.
func TestWindowsTargetNameAndSecretEncoding(t *testing.T) {
	if got, want := WindowsTargetName("Codex Auth", "cli|abc123"), "cli|abc123.Codex Auth"; got != want {
		t.Fatalf("WindowsTargetName = %q, want %q", got, want)
	}
	blob := EncodeWindowsSecret("h\u00e9llo \U0001f600")
	decoded, err := DecodeWindowsSecret(blob)
	if err != nil {
		t.Fatal(err)
	}
	if decoded != "h\u00e9llo \U0001f600" {
		t.Fatalf("decoded = %q", decoded)
	}
	if _, err := DecodeWindowsSecret([]byte{0x01}); err == nil {
		t.Fatal("odd-length blob decoded without an error")
	}
	if len(EncodeWindowsSecret("")) != 0 {
		t.Fatal("empty secret should encode to an empty blob")
	}
}

// TestOSKeyringRoundTrip writes an isolated throwaway entry in the real OS
// keyring, verifies it, and deletes it. It skips on hosts without a backend.
func TestOSKeyringRoundTrip(t *testing.T) {
	if !Available() {
		t.Skip("no durable OS keyring backend on this host")
	}
	store := New()
	const service = "codex_go_keyring_roundtrip_test"
	account := "test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := store.Load(service, account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("initial Load error = %v, want ErrNotFound", err)
	}
	if err := store.Save(service, account, "s3cret-\u03b1\U0001f600"); err != nil {
		if errors.Is(err, ErrUnavailable) {
			t.Skipf("keyring unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.Delete(service, account) })
	got, err := store.Load(service, account)
	if err != nil {
		t.Fatal(err)
	}
	if got != "s3cret-\u03b1\U0001f600" {
		t.Fatalf("loaded secret = %q", got)
	}
	removed, err := store.Delete(service, account)
	if err != nil || !removed {
		t.Fatalf("Delete = %v, %v", removed, err)
	}
	if _, err := store.Load(service, account); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after Delete error = %v, want ErrNotFound", err)
	}
}

// TestRequireIdentifierRejectsEmptyParts pins the target-name collision guard.
func TestRequireIdentifierRejectsEmptyParts(t *testing.T) {
	store := New()
	if _, err := store.Load("", "account"); err == nil {
		t.Fatal("empty service accepted")
	}
	if err := store.Save("service", "  ", "secret"); err == nil {
		t.Fatal("blank account accepted")
	}
}
