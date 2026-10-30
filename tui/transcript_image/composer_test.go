package transcript_image

import (
	"errors"
	"testing"

	"codex_go/tui/pets"
)

func TestComposerVisibleThumbnailsLikeRust(t *testing.T) {
	tests := []struct {
		width       int
		count       int
		wantVisible int
		wantHidden  int
	}{
		{width: 21, count: 5, wantVisible: 2, wantHidden: 3},
		{width: 14, count: 2, wantVisible: 2, wantHidden: 0},
		{width: 7, count: 1, wantVisible: 1, wantHidden: 0},
		{width: 6, count: 4, wantVisible: 0, wantHidden: 4},
		{width: 35, count: 10, wantVisible: 4, wantHidden: 6},
		{width: 0, count: 3, wantVisible: 0, wantHidden: 3},
	}
	for _, testCase := range tests {
		visible, hidden := ComposerVisibleThumbnails(testCase.width, testCase.count)
		if visible != testCase.wantVisible || hidden != testCase.wantHidden {
			t.Fatalf("width=%d count=%d: visible=%d hidden=%d, want %d/%d",
				testCase.width, testCase.count, visible, hidden, testCase.wantVisible, testCase.wantHidden)
		}
	}
}

func TestComposerStripHeightLikeRust(t *testing.T) {
	if got := ComposerStripHeight(21, 5); got != 4 {
		t.Fatalf("visible strip height = %d, want 4", got)
	}
	if got := ComposerStripHeight(1, 10); got != 0 {
		t.Fatalf("narrow badge-only height = %d, want 0", got)
	}
	if got := ComposerStripHeight(5, 10); got != 2 {
		t.Fatalf("badge-only height = %d, want 2", got)
	}
	if got := ComposerStripHeight(21, 0); got != 0 {
		t.Fatalf("empty strip height = %d, want 0", got)
	}
}

func TestComposerAttachmentOrderLikeRust(t *testing.T) {
	order := ComposerAttachmentOrder([]string{"[Image #1]", "[Image #2]"}, "see [Image #2] then [Image #1]")
	if len(order) != 2 || order[0] != 1 || order[1] != 0 {
		t.Fatalf("reference order = %v, want [1 0]", order)
	}
	// A placeholder missing from the draft keeps its relative order last.
	order = ComposerAttachmentOrder([]string{"[Image #2]", "[Image #1]"}, "[Image #1]")
	if len(order) != 2 || order[0] != 1 || order[1] != 0 {
		t.Fatalf("missing-placeholder order = %v, want [1 0]", order)
	}
}

func TestLoadComposerTileLikeRust(t *testing.T) {
	path := writeTestPNG(t, 40, 20)
	tile, err := LoadComposerTile(path, ComposerTileOptions{
		Protocol:   pets.ImageProtocolKitty,
		Background: RGB{},
	})
	if err != nil || tile == nil {
		t.Fatalf("LoadComposerTile() = %v, %v", tile, err)
	}
	if tile.Columns != ComposerTileColumns || tile.Rows != ComposerTileRows {
		t.Fatalf("tile cells = %dx%d", tile.Columns, tile.Rows)
	}
	if got := tile.Pixels.Bounds().Dx(); got != 48 {
		t.Fatalf("tile width = %d, want 6*8", got)
	}
	if got := tile.Pixels.Bounds().Dy(); got != 48 {
		t.Fatalf("tile height = %d, want 3*16", got)
	}
	// A dark background blends white at 0.45 into the border color.
	border := tile.Pixels.RGBAAt(0, 0)
	if border.R != 114 || border.G != 114 || border.B != 114 {
		t.Fatalf("border = %v, want the blended contrast color", border)
	}
	if fill := tile.Pixels.RGBAAt(1, 1); fill.R != 0 || fill.G != 0 || fill.B != 0 {
		t.Fatalf("interior = %v, want the composer background", fill)
	}
	// Kitty may rescale; Sixel needs the physical cell size and cannot.
	if _, err := LoadComposerTile(path, ComposerTileOptions{Protocol: pets.ImageProtocolSixel}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Sixel without a cell size error = %v", err)
	}
	if _, err := LoadComposerTile(path, ComposerTileOptions{
		Protocol: pets.ImageProtocolSixel,
		Cell:     CellSize{Width: 100, Height: 100},
	}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("oversized Sixel tile error = %v", err)
	}
}
