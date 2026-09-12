package utils

import (
	"bytes"
	"image"
	"image/gif"
	"image/png"
	"testing"
)

func promptImagePNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestLoadForPromptBytesPreservesAndResizes covers Rust load_for_prompt_bytes:
// a fit PNG/JPEG is preserved byte-for-byte, an oversized image is scaled to
// MaxDimension, and a GIF is re-encoded as PNG.
func TestLoadForPromptBytesPreservesAndResizes(t *testing.T) {
	small := promptImagePNG(t, 40, 20)
	encoded, err := LoadForPromptBytes("small.png", small, ModeResizeToFit)
	if err != nil {
		t.Fatalf("small png: %v", err)
	}
	if encoded.Mime != "image/png" || !bytes.Equal(encoded.Bytes, small) ||
		encoded.Width != 40 || encoded.Height != 20 || encoded.SourceWidth != 40 || encoded.SourceHeight != 20 {
		t.Fatalf("preserved image = %#v", encoded)
	}

	large := promptImagePNG(t, 3000, 100)
	encoded, err = LoadForPromptBytes("large.png", large, ModeResizeToFit)
	if err != nil {
		t.Fatalf("large png: %v", err)
	}
	if encoded.Width != 2048 || encoded.Height != 68 || encoded.SourceWidth != 3000 || encoded.SourceHeight != 100 {
		t.Fatalf("resized image = %#v", encoded)
	}
	config, err := png.DecodeConfig(bytes.NewReader(encoded.Bytes))
	if err != nil || config.Width != 2048 || config.Height != 68 {
		t.Fatalf("resized payload = %dx%d (err=%v)", config.Width, config.Height, err)
	}

	var gifBuf bytes.Buffer
	if err := gif.Encode(&gifBuf, image.NewRGBA(image.Rect(0, 0, 8, 4)), nil); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	encoded, err = LoadForPromptBytes("anim.gif", gifBuf.Bytes(), ModeResizeToFit)
	if err != nil {
		t.Fatalf("gif: %v", err)
	}
	if encoded.Mime != "image/png" {
		t.Fatalf("gif mime = %q, want image/png", encoded.Mime)
	}
	if config, err := png.DecodeConfig(bytes.NewReader(encoded.Bytes)); err != nil || config.Width != 8 {
		t.Fatalf("gif re-encoded payload = %#v (err=%v)", config, err)
	}
}

// TestLoadForPromptBytesOriginalAndErrors covers ModeOriginal (no resize) and
// the decode/format error paths.
func TestLoadForPromptBytesOriginalAndErrors(t *testing.T) {
	large := promptImagePNG(t, 3000, 100)
	encoded, err := LoadForPromptBytes("large.png", large, ModeOriginal)
	if err != nil {
		t.Fatalf("original mode: %v", err)
	}
	if !bytes.Equal(encoded.Bytes, large) || encoded.Width != 3000 {
		t.Fatalf("original mode image = %#v", encoded)
	}

	if _, err := LoadForPromptBytes("blob.bin", []byte("not an image"), ModeResizeToFit); err == nil {
		t.Fatal("unsupported payload must fail")
	}
	if _, err := LoadForPromptBytes("broken.png", []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2}, ModeResizeToFit); err == nil {
		t.Fatal("undecodable png must fail")
	}
}

// TestLoadForPromptBytesWithLimits covers the ResizeWithLimits entry point.
func TestLoadForPromptBytesWithLimits(t *testing.T) {
	payload := promptImagePNG(t, 2048, 2048)
	encoded, err := LoadForPromptBytesWithLimits("square.png", payload, ResizeLimits{MaxDimension: 2048, MaxPatches: 2500})
	if err != nil {
		t.Fatalf("limits mode: %v", err)
	}
	if encoded.Width != 1600 || encoded.Height != 1600 {
		t.Fatalf("limits dimensions = %dx%d, want 1600x1600", encoded.Width, encoded.Height)
	}
}

func TestDataURLFromBytesAndParse(t *testing.T) {
	input := []byte{1, 2, 3}
	url := DataURLFromBytes("image/png", input)
	mime, bytes, err := ParseDataURL(url)
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || !bytesEqual(bytes, input) {
		t.Fatalf("parsed = %q %v", mime, bytes)
	}
	_, _, err = ParseDataURL("data:image/png,AAAA")
	if err == nil {
		t.Fatalf("expected malformed data URL error")
	}
}

func TestOutputDimensions(t *testing.T) {
	width, height := OutputDimensions(4096, 2048, ModeResizeToFit, nil)
	if width != 2048 || height != 1024 {
		t.Fatalf("resize to fit = %dx%d", width, height)
	}
	width, height = OutputDimensions(4096, 2048, ModeOriginal, nil)
	if width != 4096 || height != 2048 {
		t.Fatalf("original = %dx%d", width, height)
	}
	limits := ResizeLimits{MaxDimension: 2048, MaxPatches: 2500}
	width, height = OutputDimensions(2048, 2048, ModeResizeLimits, &limits)
	if width != 1600 || height != 1600 {
		t.Fatalf("limits = %dx%d", width, height)
	}
}

func TestDimensionsFit(t *testing.T) {
	limits := ResizeLimits{MaxDimension: 2048, MaxPatches: 4}
	if !DimensionsFit(64, 64, limits) {
		t.Fatalf("64x64 should fit four patches")
	}
	if DimensionsFit(96, 96, limits) {
		t.Fatalf("96x96 should exceed four patches")
	}
}

func bytesEqual(left []byte, right []byte) bool {
	return bytes.Equal(left, right)
}
