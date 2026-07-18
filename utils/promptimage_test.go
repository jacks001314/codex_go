package utils

import (
	"bytes"
	"testing"
)

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
