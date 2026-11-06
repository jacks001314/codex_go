//go:build linux

package tea

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 5, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 5; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 30), uint8(y * 60), 200, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A clipboard that already holds PNG is written through byte for byte, so the
// pasted file can be checksum-compared with the source.
func TestClipboardImagePNGPassthroughKeepsBytes(t *testing.T) {
	source := testPNGBytes(t)
	path, err := writeClipboardImagePNG(source, "image/png")
	if err != nil {
		t.Fatalf("writeClipboardImagePNG: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source) {
		t.Fatalf("pasted bytes differ from the clipboard payload (%d vs %d bytes)", len(got), len(source))
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(got))
	if err != nil || cfg.Width != 5 || cfg.Height != 3 {
		t.Fatalf("decoded config = %#v, %v", cfg, err)
	}
}

// Non-PNG payloads are decoded and re-encoded like Rust's write_to(Png).
func TestClipboardImageJPEGReencodesToPNG(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	path, err := writeClipboardImagePNG(buf.Bytes(), "image/jpeg")
	if err != nil {
		t.Fatalf("writeClipboardImagePNG(jpeg): %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("re-encoded payload is not PNG: %q", data[:8])
	}
}

func TestClipboardImageCorruptPNGIsEncodeFailed(t *testing.T) {
	_, err := writeClipboardImagePNG([]byte("\x89PNG\r\n\x1a\nnot-a-real-png"), "image/png")
	var pasteErr codextui.PasteImageError
	if !errors.As(err, &pasteErr) || pasteErr.Kind != codextui.PasteImageEncodeFailed {
		t.Fatalf("err = %v, want PasteImageEncodeFailed", err)
	}
}

// No display means no helper can run: ClipboardUnavailable, not NoImage, so the
// TUI reports the failure instead of pasting text.
func TestPasteClipboardImageWithoutDisplayIsUnavailable(t *testing.T) {
	_, err := pasteClipboardImage(context.Background(), nil)
	var pasteErr codextui.PasteImageError
	if !errors.As(err, &pasteErr) || pasteErr.Kind != codextui.PasteImageClipboardUnavailable {
		t.Fatalf("err = %v, want PasteImageClipboardUnavailable", err)
	}
}

func TestPasteClipboardImageMissingHelperIsUnavailable(t *testing.T) {
	tool := clipboardImageTool{name: "codex-no-such-clipboard-helper"}
	_, err := pasteClipboardImage(context.Background(), []clipboardImageTool{tool})
	var pasteErr codextui.PasteImageError
	if !errors.As(err, &pasteErr) || pasteErr.Kind != codextui.PasteImageClipboardUnavailable {
		t.Fatalf("err = %v, want PasteImageClipboardUnavailable", err)
	}
}

func writeHelperScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clipboard-helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPasteClipboardImageWithoutTargetIsNoImage(t *testing.T) {
	script := writeHelperScript(t, `case "$1" in
  targets) printf 'UTF8_STRING\nTEXT\n'; exit 0;;
  *) exit 1;;
esac`)
	tool := clipboardImageTool{
		name:    script,
		targets: []string{"targets"},
		read:    func(mime string) []string { return []string{"read", mime} },
		files:   []string{"files"},
	}
	_, err := pasteClipboardImage(context.Background(), []clipboardImageTool{tool})
	var pasteErr codextui.PasteImageError
	if !errors.As(err, &pasteErr) || pasteErr.Kind != codextui.PasteImageNoImage {
		t.Fatalf("err = %v, want PasteImageNoImage", err)
	}
}

// End-to-end through the helper seam: TARGETS advertises image/png and the read
// returns it verbatim.
func TestPasteClipboardImageReadsPNGThroughHelper(t *testing.T) {
	source := testPNGBytes(t)
	sourcePath := filepath.Join(t.TempDir(), "source.png")
	if err := os.WriteFile(sourcePath, source, 0o600); err != nil {
		t.Fatal(err)
	}
	script := writeHelperScript(t, `case "$1" in
  targets) printf 'TARGETS\nimage/png\n'; exit 0;;
  files) exit 1;;
  read) cat `+sourcePath+`; exit 0;;
  *) exit 1;;
esac`)
	tool := clipboardImageTool{
		name:    script,
		targets: []string{"targets"},
		read:    func(mime string) []string { return []string{"read"} },
		files:   []string{"files"},
	}
	path, err := pasteClipboardImage(context.Background(), []clipboardImageTool{tool})
	if err != nil {
		t.Fatalf("pasteClipboardImage: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(path)) })
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, source) {
		t.Fatal("helper payload was not written through verbatim")
	}
}

func TestImagePasteFallsBackToTextOnlyForNoImage(t *testing.T) {
	if !imagePasteFallsBackToText(codextui.PasteImageError{Kind: codextui.PasteImageNoImage}) {
		t.Fatal("NoImage must fall back to text")
	}
	for _, kind := range []codextui.PasteImageErrorKind{
		codextui.PasteImageClipboardUnavailable,
		codextui.PasteImageEncodeFailed,
		codextui.PasteImageIOError,
	} {
		if imagePasteFallsBackToText(codextui.PasteImageError{Kind: kind}) {
			t.Fatalf("%s must not fall back to text", kind)
		}
	}
	if imagePasteFallsBackToText(errors.New("plain error")) {
		t.Fatal("an unclassified failure must not fall back to text")
	}
}

// Rust app/clipboard.rs: a clipboard backend failure surfaces as an error cell
// and never silently pastes text.
func TestCtrlVImageBackendFailureShowsErrorLikeRust(t *testing.T) {
	previousImage, previousText := readClipboardImageForPaste, readClipboardTextForPaste
	t.Cleanup(func() { readClipboardImageForPaste, readClipboardTextForPaste = previousImage, previousText })
	readClipboardImageForPaste = func() (string, error) {
		return "", codextui.PasteImageError{Kind: codextui.PasteImageClipboardUnavailable, Message: "no X11 or Wayland display"}
	}
	readClipboardTextForPaste = func() (string, error) {
		t.Fatal("clipboard text must not be read when the image backend fails")
		return "", nil
	}
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.Update(key(keymsg.KeyCtrlV))
	if !strings.HasPrefix(model.notice, "Failed to paste image: clipboard unavailable:") {
		t.Fatalf("notice = %q", model.notice)
	}
	if got := model.composerText(); got != "" {
		t.Fatalf("composer = %q, want empty", got)
	}
}
