package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bottompane "codex_go/tui/bottom_pane"
	codextea "codex_go/tui/tea"
)

func testPNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		img.Set(x, 0, color.RGBA{R: uint8(x % 255), G: 40, B: 90, A: 255})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestAgentsOverviewTaskInputsLocalAndRemote covers Rust #44027: the first task
// prompt carries the images (up to date locally, snapshotted for a remote
// workspace) followed by the prompt text, and a small PNG is preserved
// byte-for-byte in the snapshot.
func TestAgentsOverviewTaskInputsLocalAndRemote(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "chart.png")
	source := testPNG(t, 8, 4)
	if err := os.WriteFile(imagePath, source, 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	request := codextea.SubmitRequest{
		Prompt: "inspect the chart",
		Attachments: []bottompane.ComposerAttachment{
			{Kind: bottompane.AttachmentImage, Path: imagePath},
			{Kind: bottompane.AttachmentRemoteImage, URL: "https://example.com/a.png"},
		},
	}

	local, err := agentsOverviewTaskInputs(request, false)
	if err != nil {
		t.Fatalf("local inputs: %v", err)
	}
	if len(local) != 3 {
		t.Fatalf("local inputs = %#v", local)
	}
	if local[0].Type != "localImage" || local[0].Path != imagePath {
		t.Fatalf("local image input = %#v", local[0])
	}
	if local[1].Type != "image" || local[1].URL != "https://example.com/a.png" {
		t.Fatalf("remote image input = %#v", local[1])
	}
	if local[2].Type != "text" || local[2].Text != "inspect the chart" {
		t.Fatalf("prompt input = %#v", local[2])
	}

	remote, err := agentsOverviewTaskInputs(request, true)
	if err != nil {
		t.Fatalf("remote inputs: %v", err)
	}
	if remote[0].Type != "image" || !strings.HasPrefix(remote[0].URL, "data:image/png;base64,") {
		t.Fatalf("snapshotted image input = %#v", remote[0])
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(remote[0].URL, "data:image/png;base64,"))
	if err != nil || !bytes.Equal(decoded, source) {
		t.Fatalf("snapshot payload differs from the source (err=%v)", err)
	}
}

// TestLocalImageDataURLPreservesAndConverts covers Rust #44027's snapshot
// pipeline: PNG/JPEG within the dimension limit are preserved byte-for-byte
// with their detected MIME, while GIF is re-encoded as PNG.
func TestLocalImageDataURLPreservesAndConverts(t *testing.T) {
	dir := t.TempDir()

	pngPath := filepath.Join(dir, "chart.png")
	pngSource := testPNG(t, 8, 4)
	if err := os.WriteFile(pngPath, pngSource, 0o644); err != nil {
		t.Fatalf("write png: %v", err)
	}
	url, err := localImageDataURL(pngPath)
	if err != nil {
		t.Fatalf("png snapshot: %v", err)
	}
	if decoded := decodeDataURLPayload(t, url, "image/png"); !bytes.Equal(decoded, pngSource) {
		t.Fatal("a small png must be preserved byte-for-byte")
	}

	jpegPath := filepath.Join(dir, "photo.JPEG")
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, image.NewRGBA(image.Rect(0, 0, 8, 4)), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	if err := os.WriteFile(jpegPath, jpegBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("write jpeg: %v", err)
	}
	url, err = localImageDataURL(jpegPath)
	if err != nil {
		t.Fatalf("jpeg snapshot: %v", err)
	}
	if decoded := decodeDataURLPayload(t, url, "image/jpeg"); !bytes.Equal(decoded, jpegBuf.Bytes()) {
		t.Fatal("a small jpeg must be preserved byte-for-byte")
	}

	gifPath := filepath.Join(dir, "anim.gif")
	var gifBuf bytes.Buffer
	if err := gif.Encode(&gifBuf, image.NewRGBA(image.Rect(0, 0, 8, 4)), nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	if err := os.WriteFile(gifPath, gifBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("write gif: %v", err)
	}
	url, err = localImageDataURL(gifPath)
	if err != nil {
		t.Fatalf("gif snapshot: %v", err)
	}
	decodeDataURLPayload(t, url, "image/png")

	if _, err := localImageDataURL(filepath.Join(dir, "missing.png")); err == nil {
		t.Fatal("missing file should fail")
	}
	invalidPath := filepath.Join(dir, "invalid.png")
	if err := os.WriteFile(invalidPath, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3, 4}, 0o644); err != nil {
		t.Fatalf("write invalid: %v", err)
	}
	if _, err := localImageDataURL(invalidPath); err == nil {
		t.Fatal("undecodable image bytes should fail")
	}
}

// TestLocalImageDataURLResizesLargeImages covers Rust ResizeToFit: an image over
// 2048px is scaled to fit instead of being rejected.
func TestLocalImageDataURLResizesLargeImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wide.png")
	if err := os.WriteFile(path, testPNG(t, 3000, 100), 0o644); err != nil {
		t.Fatalf("write image: %v", err)
	}
	url, err := localImageDataURL(path)
	if err != nil {
		t.Fatalf("large image snapshot: %v", err)
	}
	decoded := decodeDataURLPayload(t, url, "image/png")
	config, err := png.DecodeConfig(bytes.NewReader(decoded))
	if err != nil {
		t.Fatalf("decode resized png: %v", err)
	}
	if config.Width != 2048 || config.Height != 68 {
		t.Fatalf("resized dimensions = %dx%d, want 2048x68", config.Width, config.Height)
	}
}

func decodeDataURLPayload(t *testing.T, url string, wantMIME string) []byte {
	t.Helper()
	prefix := "data:" + wantMIME + ";base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("url = %q, want prefix %q", url, prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	return decoded
}
