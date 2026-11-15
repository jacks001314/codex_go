package tui

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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
	if runtime.GOOS == "linux" {
		// Conversion depends on /bin/wslpath and this process's mount
		// namespace; the deterministic helper coverage lives below.
		t.Skip("drive-path expectations are environment dependent on Linux")
	}
	for _, input := range []string{
		`C:\Users\Alice\Pictures\cat.png`,
		`C:/Users/Alice/Pictures/cat.png`,
		`"D:\My Files\image.png"`,
		`'E:\one\two.jpg'`,
	} {
		got, ok := NormalizePastedPathWithWSL(input, true)
		want := filepath.Clean(stripSimpleQuotes(input))
		if !ok || got != want {
			t.Fatalf("NormalizePastedPathWithWSL(%q) = %q ok=%v, want %q", input, got, ok, want)
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
	dir := t.TempDir()
	helper := filepath.Join(dir, "wslpath")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	mountinfo := []byte("36 25 0:32 / /mnt/c rw,relatime - 9p drvfs rw\n")
	readMount := func(string) ([]byte, error) { return mountinfo, nil }

	t.Run("helper output", func(t *testing.T) {
		run := func(string, []string, time.Duration) ([]byte, error) {
			return []byte("/mnt/d/My Files/image.png\n"), nil
		}
		got, ok := convertWindowsPathToWSL(`D:\My Files\image.png`, helper, readMount, run)
		if !ok || got != "/mnt/d/My Files/image.png" {
			t.Fatalf("helper conversion = %q ok=%v", got, ok)
		}
	})
	t.Run("helper requires newline and absolute result", func(t *testing.T) {
		for name, output := range map[string][]byte{
			"missing newline": []byte("/mnt/d/image.png"),
			"relative":        []byte("image.png\n"),
			"empty":           nil,
		} {
			run := func(string, []string, time.Duration) ([]byte, error) { return output, nil }
			if got, ok := convertWindowsPathToWSL(`D:\image.png`, helper, readMount, run); ok {
				t.Fatalf("%s conversion = %q, want failure", name, got)
			}
		}
	})
	t.Run("helper failure", func(t *testing.T) {
		run := func(string, []string, time.Duration) ([]byte, error) { return nil, errors.New("deadline") }
		if got, ok := convertWindowsPathToWSL(`D:\image.png`, helper, readMount, run); ok {
			t.Fatalf("failed helper conversion = %q", got)
		}
	})
	t.Run("missing helper falls back to confirmed mount", func(t *testing.T) {
		missing := filepath.Join(dir, "missing")
		run := func(string, []string, time.Duration) ([]byte, error) { t.Fatal("helper must not run"); return nil, nil }
		got, ok := convertWindowsPathToWSL(`C:\Temp\clip.png`, missing, readMount, run)
		if !ok || got != "/mnt/c/Temp/clip.png" {
			t.Fatalf("fallback conversion = %q ok=%v", got, ok)
		}
		got, ok = convertWindowsPathToWSL(`C:\a\..\b.png`, missing, readMount, run)
		if !ok || got != "/mnt/c/b.png" {
			t.Fatalf("fallback cleanup = %q ok=%v", got, ok)
		}
	})
	t.Run("missing helper rejects unmounted or traversing paths", func(t *testing.T) {
		missing := filepath.Join(dir, "missing")
		run := func(string, []string, time.Duration) ([]byte, error) { return nil, nil }
		for name, input := range map[string]string{
			"unmounted drive":  `Z:\clip.png`,
			"above mount root": `C:\..\clip.png`,
		} {
			if got, ok := convertWindowsPathToWSL(input, missing, readMount, run); ok {
				t.Fatalf("%s conversion = %q, want failure", name, got)
			}
		}
	})
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
