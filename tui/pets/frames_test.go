package pets

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparePNGFramesSlicesSpritesheet(t *testing.T) {
	dir := t.TempDir()
	sheetPath := filepath.Join(dir, "spritesheet.png")
	// 2 columns x 1 row of 1x1 frames: red then green.
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	file, err := os.Create(sheetPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode: %v", err)
	}
	file.Close()

	frameDir := filepath.Join(dir, "frames")
	frames, err := PreparePNGFrames(sheetPath, frameDir, 1, 1, 2, 1)
	if err != nil {
		t.Fatalf("PreparePNGFrames: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frame count = %d, want 2", len(frames))
	}
	if filepath.Base(frames[0]) != "frame_000.png" || filepath.Base(frames[1]) != "frame_001.png" {
		t.Fatalf("frame names = %q, %q", frames[0], frames[1])
	}
	for _, path := range frames {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing frame %s: %v", path, err)
		}
	}
	if !samePixel(t, frames[0], 0, 0, color.RGBA{R: 255, A: 255}) {
		t.Fatalf("frame 0 not red")
	}
	if !samePixel(t, frames[1], 0, 0, color.RGBA{G: 255, A: 255}) {
		t.Fatalf("frame 1 not green")
	}

	// A complete cache is reused as-is (stale extra files are left alone,
	// matching Rust). An incomplete cache regenerates and clears stale files.
	stale := filepath.Join(frameDir, "frame_999.png")
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatalf("stale: %v", err)
	}
	framesAgain, err := PreparePNGFrames(sheetPath, frameDir, 1, 1, 2, 1)
	if err != nil {
		t.Fatalf("second PreparePNGFrames: %v", err)
	}
	if len(framesAgain) != 2 {
		t.Fatalf("second frame count = %d", len(framesAgain))
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("complete cache should keep stale frame: %v", err)
	}

	if err := os.Remove(framesAgain[0]); err != nil {
		t.Fatalf("remove frame: %v", err)
	}
	framesRegenerated, err := PreparePNGFrames(sheetPath, frameDir, 1, 1, 2, 1)
	if err != nil {
		t.Fatalf("regenerate PreparePNGFrames: %v", err)
	}
	if _, err := os.Stat(framesRegenerated[0]); err != nil {
		t.Fatalf("missing regenerated frame: %v", err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("stale frame should have been removed during regeneration")
	}
}

func TestPreparePNGFramesRejectsFrameBeyondSpritesheet(t *testing.T) {
	dir := t.TempDir()
	sheetPath := filepath.Join(dir, "spritesheet.png")
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	file, err := os.Create(sheetPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	png.Encode(file, img)
	file.Close()
	if _, err := PreparePNGFrames(sheetPath, filepath.Join(dir, "frames"), 2, 1, 1, 1); err == nil {
		t.Fatal("expected out-of-bounds error")
	}
}

func TestPrepareSixelFrameEncodesCachedSixel(t *testing.T) {
	dir := t.TempDir()
	framePath := filepath.Join(dir, "frame_000.png")
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 1, color.RGBA{G: 255, A: 255})
	file, err := os.Create(framePath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	png.Encode(file, img)
	file.Close()

	sixelDir := filepath.Join(dir, "sixel")
	path, err := PrepareSixelFrame(framePath, sixelDir, 1)
	if err != nil {
		t.Fatalf("PrepareSixelFrame: %v", err)
	}
	if !strings.HasSuffix(path, "_h1_v2.six") {
		t.Fatalf("cache path = %q", path)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read sixel: %v", err)
	}
	if !strings.HasPrefix(string(payload), "\x1bP") || !strings.HasSuffix(string(payload), "\x1b\\") {
		t.Fatalf("sixel payload missing DCS wrapper: %q", string(payload[:min(len(payload), 8)]))
	}

	// Second call reuses the cache.
	pathAgain, err := PrepareSixelFrame(framePath, sixelDir, 1)
	if err != nil {
		t.Fatalf("second PrepareSixelFrame: %v", err)
	}
	if pathAgain != path {
		t.Fatalf("cache path changed: %q vs %q", pathAgain, path)
	}
}

func samePixel(t *testing.T, path string, x int, y int, want color.RGBA) bool {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open frame: %v", err)
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		t.Fatalf("decode frame: %v", err)
	}
	r, g, b, a := decoded.At(x, y).RGBA()
	return uint8(r>>8) == want.R && uint8(g>>8) == want.G && uint8(b>>8) == want.B && uint8(a>>8) == want.A
}
