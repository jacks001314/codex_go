package tui

import (
	"path/filepath"
	"testing"
)

func TestPasteImageErrorMessagesMatchRust(t *testing.T) {
	cases := []struct {
		err  PasteImageError
		want string
	}{
		{PasteImageError{Kind: PasteImageClipboardUnavailable, Message: "backend missing"}, "clipboard unavailable: backend missing"},
		{PasteImageError{Kind: PasteImageNoImage, Message: "empty"}, "no image on clipboard: empty"},
		{PasteImageError{Kind: PasteImageEncodeFailed, Message: "bad rgba"}, "could not encode image: bad rgba"},
		{PasteImageError{Kind: PasteImageIOError, Message: "disk"}, "io error: disk"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Fatalf("PasteImageError(%s) = %q, want %q", tc.err.Kind, got, tc.want)
		}
	}
}

func TestNormalizePastedPathWindowsWSLMatchesRust(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{`C:\Users\Alice\Pictures\cat.png`, `/mnt/c/Users/Alice/Pictures/cat.png`},
		{`C:/Users/Alice/Pictures/cat.png`, `/mnt/c/Users/Alice/Pictures/cat.png`},
		{`"D:\My Files\image.png"`, `/mnt/d/My Files/image.png`},
		{`'E:\one\two.jpg'`, `/mnt/e/one/two.jpg`},
	}
	for _, tc := range cases {
		got, ok := NormalizePastedPathWithWSL(tc.input, true)
		if !ok || got != tc.want {
			t.Fatalf("NormalizePastedPathWithWSL(%q) = %q ok=%v, want %q", tc.input, got, ok, tc.want)
		}
	}
}

func TestNormalizePastedPathUNCAndShellEscapesMatchRust(t *testing.T) {
	unc := `\\server\share\path\image.png`
	got, ok := NormalizePastedPathWithWSL(unc, true)
	if !ok || got != filepath.Clean(unc) {
		t.Fatalf("UNC path = %q ok=%v, want %q", got, ok, filepath.Clean(unc))
	}
	if got, ok := NormalizePastedPathWithWSL(`/home/user/My\ File.png`, false); !ok || got != filepath.Clean("/home/user/My File.png") {
		t.Fatalf("shell escaped path = %q ok=%v", got, ok)
	}
	if _, ok := NormalizePastedPathWithWSL(`/home/a.png /home/b.png`, false); ok {
		t.Fatal("multi-token pasted path ok=true")
	}
}

func TestConvertWindowsPathToWSLMatchesRust(t *testing.T) {
	got, ok := ConvertWindowsPathToWSL(`C:\Temp\clip.png`)
	if !ok || got != "/mnt/c/Temp/clip.png" {
		t.Fatalf("ConvertWindowsPathToWSL drive = %q ok=%v", got, ok)
	}
	got, ok = ConvertWindowsPathToWSL(`Z:/a//b/c.png`)
	if !ok || got != "/mnt/z/a/b/c.png" {
		t.Fatalf("ConvertWindowsPathToWSL slash = %q ok=%v", got, ok)
	}
	if _, ok := ConvertWindowsPathToWSL(`\\server\share\clip.png`); ok {
		t.Fatal("ConvertWindowsPathToWSL accepted UNC path")
	}
}

func TestIsProbablyWSLFromMatchesRustChecks(t *testing.T) {
	if !IsProbablyWSLFrom("Linux version 5.15.90.1-microsoft-standard-WSL2", nil) {
		t.Fatal("microsoft proc version should detect WSL")
	}
	if !IsProbablyWSLFrom("custom kernel", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}) {
		t.Fatal("WSL_DISTRO_NAME should detect WSL")
	}
	if !IsProbablyWSLFrom("custom kernel", map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"}) {
		t.Fatal("WSL_INTEROP should detect WSL")
	}
	if IsProbablyWSLFrom("Linux version generic", map[string]string{}) {
		t.Fatal("generic Linux should not detect WSL")
	}
}
