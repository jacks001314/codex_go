package transcript_image

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/tui/pets"
)

func writeTestPNG(t *testing.T, width int, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	path := filepath.Join(t.TempDir(), "preview.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatalf("encode png: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close png: %v", err)
	}
	return path
}

func TestLoadBoundsThumbnailLikeRust(t *testing.T) {
	path := writeTestPNG(t, 200, 120)
	imageData, err := Load(path, 80, pets.ImageProtocolKitty, CellSize{})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if imageData.Columns == 0 || imageData.Columns > 48 || imageData.Rows == 0 || imageData.Rows > 6 {
		t.Fatalf("thumbnail cells = %dx%d", imageData.Columns, imageData.Rows)
	}
	if imageData.Pixels.Bounds().Dx() > 384 || imageData.Pixels.Bounds().Dy() > 160 {
		t.Fatalf("pixels = %v", imageData.Pixels.Bounds())
	}
	if FirstImageID != 0xC100 {
		t.Fatalf("FirstImageID = %#x", FirstImageID)
	}

	narrow, err := Load(path, 10, pets.ImageProtocolKitty, CellSize{})
	if err != nil {
		t.Fatalf("load narrow: %v", err)
	}
	if narrow.Columns > 8 {
		t.Fatalf("narrow columns = %d", narrow.Columns)
	}
}

func TestLoadFallbacksAndLimitsLikeRust(t *testing.T) {
	path := writeTestPNG(t, 20, 20)
	if _, err := Load(path, 80, pets.ImageProtocolSixel, CellSize{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("sixel without cell size = %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.png"), 80, pets.ImageProtocolKitty, CellSize{}); err == nil {
		t.Fatal("missing image loaded")
	}

	large := filepath.Join(t.TempDir(), "large.png")
	file, err := os.Create(large)
	if err != nil {
		t.Fatalf("create large: %v", err)
	}
	if err := file.Truncate(MaxEncodedBytes + 1); err != nil {
		file.Close()
		t.Fatalf("truncate large: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close large: %v", err)
	}
	if _, err := Load(large, 80, pets.ImageProtocolKitty, CellSize{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized encoded image = %v", err)
	}

	largeRaster := filepath.Join(t.TempDir(), "large-raster.png")
	rasterFile, err := os.Create(largeRaster)
	if err != nil {
		t.Fatalf("create large raster: %v", err)
	}
	if err := png.Encode(rasterFile, image.NewGray(image.Rect(0, 0, 4000, 3000))); err != nil {
		rasterFile.Close()
		t.Fatalf("encode large raster: %v", err)
	}
	if err := rasterFile.Close(); err != nil {
		t.Fatalf("close large raster: %v", err)
	}
	if _, err := Load(largeRaster, 80, pets.ImageProtocolKitty, CellSize{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized decoded raster = %v", err)
	}
}

func TestPlacementClipsToViewportAndSourceRowsLikeRust(t *testing.T) {
	pixels := image.NewRGBA(image.Rect(0, 0, 100, 96))
	placement := Placement{
		Image: &TranscriptImage{
			Protocol:   pets.ImageProtocolKitty,
			Columns:    13,
			Rows:       6,
			Pixels:     pixels,
			CellWidth:  8,
			CellHeight: 16,
		},
		FirstRow: 10,
		Height:   6,
	}
	first, rows, ok := placement.VisibleRows(12, 3)
	if !ok || first != 2 || rows != 3 {
		t.Fatalf("visible rows = %d,%d,%v", first, rows, ok)
	}
	top, bottom, ok := placement.ClipSourceRows(first, rows)
	if !ok || top != 32 || bottom != 80 {
		t.Fatalf("source clip = %d,%d,%v", top, bottom, ok)
	}
	if _, _, ok := placement.VisibleRows(20, 5); ok {
		t.Fatal("offscreen placement reported visible rows")
	}
}

func TestEncodeKittyAndSixelPayloadsLikeRust(t *testing.T) {
	path := writeTestPNG(t, 40, 24)
	kitty, err := Load(path, 80, pets.ImageProtocolKitty, CellSize{})
	if err != nil {
		t.Fatalf("load kitty: %v", err)
	}
	id := FirstImageID
	encoded, err := kitty.Encode(3, 4, 0, int(kitty.Rows), id, nil)
	if err != nil {
		t.Fatalf("encode kitty: %v", err)
	}
	if !strings.Contains(string(encoded.Payload), "\x1b_Ga=T") ||
		!strings.Contains(string(encoded.Payload), ",i=49408") {
		t.Fatalf("kitty payload = %q", encoded.Payload)
	}
	if encoded.TempPath != "" {
		t.Fatalf("kitty temp path = %q", encoded.TempPath)
	}
	var out strings.Builder
	if err := encoded.Write(&out, 3, 4); err != nil {
		t.Fatalf("write kitty: %v", err)
	}
	if !strings.HasPrefix(out.String(), "\x1b7\x1b[5;4H") || !strings.HasSuffix(out.String(), "\x1b8") {
		t.Fatalf("kitty positioned payload = %q", out.String())
	}

	sixel, err := Load(path, 80, pets.ImageProtocolSixel, CellSize{Width: 10, Height: 20})
	if err != nil {
		t.Fatalf("load sixel: %v", err)
	}
	sixelPayload, err := sixel.Encode(0, 0, 1, 1, id, nil)
	if err != nil {
		t.Fatalf("encode sixel: %v", err)
	}
	if len(sixelPayload.Payload) == 0 || !strings.Contains(string(sixelPayload.Payload), "\x1bP") {
		t.Fatalf("sixel payload = %q", sixelPayload.Payload)
	}
}
