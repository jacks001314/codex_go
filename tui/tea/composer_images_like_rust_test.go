package tea

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/tui/bottom_pane"
)

func writeComposerTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 200, G: 40, B: 40, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

// TestComposerThumbnailsFollowReferenceOrderLikeRust covers Rust #58261: the
// strip lists attachments in textarea reference order, not label order, and
// reports the ones that do not fit as +N without touching the draft text.
func TestComposerThumbnailsFollowReferenceOrderLikeRust(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.png")
	second := filepath.Join(dir, "second.png")
	writeComposerTestPNG(t, first)
	writeComposerTestPNG(t, second)
	model := NewModel(nil, Options{
		LocalSession: true,
		PetEnv:       map[string]string{"KITTY_WINDOW_ID": "1"},
		Width:        40,
		Height:       20,
	})
	model.composer.SetValue("[Image #2] then [Image #1]")
	model.composerElements = []ComposerTextElement{
		{Start: 0, End: 10, Placeholder: "[Image #2]"},
		{Start: 16, End: 26, Placeholder: "[Image #1]"},
	}
	model.attachments = []bottompane.ComposerAttachment{
		{Kind: bottompane.AttachmentImage, Path: first},
		{Kind: bottompane.AttachmentImage, Path: second},
	}
	strip, tiles := model.composerThumbnailStrip(20)
	if len(tiles) != 2 {
		t.Fatalf("tiles = %d, want 2", len(tiles))
	}
	if tiles[0].placeholder != "[Image #2]" || tiles[1].placeholder != "[Image #1]" {
		t.Fatalf("tile order = %q, %q", tiles[0].placeholder, tiles[1].placeholder)
	}
	if tiles[0].image == nil || tiles[1].image == nil {
		t.Fatal("supported terminal did not decode the tiles")
	}
	if !strings.Contains(strip, "#2]") || !strings.Contains(strip, "#1]") {
		t.Fatalf("strip = %q", strip)
	}
	if strings.TrimSpace(model.composer.Value()) != "[Image #2] then [Image #1]" {
		t.Fatalf("draft changed: %q", model.composer.Value())
	}
	// Width 10 fits one six-column tile and hides the other behind +1.
	_, tiles = model.composerThumbnailStrip(10)
	if len(tiles) != 1 {
		t.Fatalf("narrow tiles = %d, want 1", len(tiles))
	}
	narrowStrip, _ := model.composerThumbnailStrip(10)
	if !strings.Contains(narrowStrip, "+1") {
		t.Fatalf("narrow strip = %q, want the +1 badge", narrowStrip)
	}
}
