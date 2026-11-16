package tui

import (
	"errors"
	"testing"
)

// Rust #53453: on WSL the Windows clipboard runs first and the native WSLg
// clipboard is its fallback; a double failure keeps both reasons.
func TestComposeWSLClipboardWriteLikeRust(t *testing.T) {
	var order []string
	wsl := func(text string) error {
		order = append(order, "wsl:"+text)
		return nil
	}
	native := func(text string) error {
		order = append(order, "native:"+text)
		return nil
	}
	if err := ComposeWSLClipboardWrite(wsl, native)("abc"); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if len(order) != 1 || order[0] != "wsl:abc" {
		t.Fatalf("successful WSL copy calls = %v, want only the Windows writer", order)
	}

	order = nil
	failing := func(text string) error {
		order = append(order, "wsl:"+text)
		return errors.New("powershell unavailable")
	}
	if err := ComposeWSLClipboardWrite(failing, native)("abc"); err != nil {
		t.Fatalf("native fallback: %v", err)
	}
	if len(order) != 2 || order[1] != "native:abc" {
		t.Fatalf("fallback calls = %v", order)
	}

	err := ComposeWSLClipboardWrite(failing, func(string) error { return errors.New("native unavailable") })("abc")
	if err == nil || err.Error() != "Windows clipboard: powershell unavailable; native clipboard: native unavailable" {
		t.Fatalf("double failure = %v", err)
	}

	if ComposeWSLClipboardWrite(nil, native) == nil {
		t.Fatal("a nil WSL writer must keep the native writer")
	}
}