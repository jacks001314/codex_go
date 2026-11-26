package transcript_image

// Mirrors Rust #61919 transcript_image.rs PreviewKind::Generated: a generated
// preview spans the body and its pixel cap follows the image aspect, while an
// inspected preview keeps the 48-column, six-row budget.

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"codex_go/tui/pets"
)

func writeGeneratedTestPNG(t *testing.T, width int, height int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "generated.png")
	source := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 60, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	if err := png.Encode(file, source); err != nil {
		file.Close()
		t.Fatalf("encode image: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close image: %v", err)
	}
	return path
}

func TestGeneratedPreviewSizesFollowAspectLikeRust(t *testing.T) {
	cell := CellSize{Width: 8, Height: 16}
	cases := []struct {
		name           string
		sourceWidth    int
		sourceHeight   int
		columns        int
		virtualBase    bool
		wantPixelsWide int
		wantPixelsHigh int
	}{
		// 2:1 or flatter takes the 768px cap; 80 columns of body is 624px.
		{name: "two-to-one", sourceWidth: 1600, sourceHeight: 800, columns: 80, wantPixelsWide: 624, wantPixelsHigh: 312},
		// Wide body is the binding cap once it exceeds the aspect cap.
		{name: "aspect-cap", sourceWidth: 1600, sourceHeight: 800, columns: 200, wantPixelsWide: 768, wantPixelsHigh: 384},
		// 1.5:1 takes the 480px cap.
		{name: "three-to-two", sourceWidth: 1500, sourceHeight: 1000, columns: 200, wantPixelsWide: 480, wantPixelsHigh: 320},
		// A square takes the 400px cap.
		{name: "square", sourceWidth: 1000, sourceHeight: 1000, columns: 200, wantPixelsWide: 400, wantPixelsHigh: 400},
		// The height ceiling clips a tall image inside the 400px aspect cap.
		{name: "tall", sourceWidth: 300, sourceHeight: 1200, columns: 200, wantPixelsWide: 100, wantPixelsHigh: 400},
		// A fullscreen virtual image pane keeps the six-cell row budget.
		{name: "virtual-base", sourceWidth: 1000, sourceHeight: 1000, columns: 200, virtualBase: true, wantPixelsWide: 96, wantPixelsHigh: 96},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeGeneratedTestPNG(t, tc.sourceWidth, tc.sourceHeight)
			imageData, err := LoadGenerated(path, tc.columns, pets.ImageProtocolKitty, cell, tc.virtualBase)
			if err != nil {
				t.Fatalf("load generated: %v", err)
			}
			bounds := imageData.Pixels.Bounds()
			if bounds.Dx() != tc.wantPixelsWide || bounds.Dy() != tc.wantPixelsHigh {
				t.Fatalf("raster = %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), tc.wantPixelsWide, tc.wantPixelsHigh)
			}
			if int(imageData.Columns) != (tc.wantPixelsWide+7)/8 || int(imageData.Rows) != (tc.wantPixelsHigh+15)/16 {
				t.Fatalf("cells = %dx%d", imageData.Columns, imageData.Rows)
			}
		})
	}
}

func TestInspectedPreviewKeepsBoundedBudgetLikeRust(t *testing.T) {
	path := writeGeneratedTestPNG(t, 1600, 800)
	imageData, err := Load(path, 200, pets.ImageProtocolKitty, CellSize{Width: 8, Height: 16})
	if err != nil {
		t.Fatalf("load inspected: %v", err)
	}
	bounds := imageData.Pixels.Bounds()
	if bounds.Dx() > 384 || bounds.Dy() > 160 {
		t.Fatalf("inspected raster = %dx%d", bounds.Dx(), bounds.Dy())
	}
	if imageData.Columns > 48 {
		t.Fatalf("inspected columns = %d", imageData.Columns)
	}
}

func TestGeneratedPreviewNeedsAProtocolAndColumns(t *testing.T) {
	path := writeGeneratedTestPNG(t, 40, 40)
	if _, err := LoadGenerated(path, 1, pets.ImageProtocolKitty, CellSize{}, false); err == nil {
		t.Fatal("a body narrower than two columns produced a preview")
	}
	if _, err := LoadGenerated(path, 80, pets.ImageProtocolSixel, CellSize{}, false); err == nil {
		t.Fatal("Sixel without a measured cell produced a preview")
	}
}
