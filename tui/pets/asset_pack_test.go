package pets

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// testSpritesheetPNG returns a solid PNG spritesheet of the given size with a
// distinguishing marker pixel so frame extraction can be verified.
func testSpritesheetPNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode test spritesheet: %v", err)
	}
	return buffer.Bytes()
}

func TestEnsureBuiltinPetDownloadsAndCaches(t *testing.T) {
	codexHome := t.TempDir()
	payload := testSpritesheetPNG(t, SpritesheetWidth, SpritesheetHeight)
	pet, _ := BuiltinPet("dewey")
	fetches := 0
	fetch := func(url string, maxBytes int64) ([]byte, error) {
		fetches++
		if url != BuiltinPetURL(pet) {
			t.Fatalf("fetch url = %q, want %q", url, BuiltinPetURL(pet))
		}
		return payload, nil
	}

	path, err := EnsureBuiltinPet(codexHome, pet, fetch)
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("fetches after first = %d, want 1", fetches)
	}
	if path != BuiltinSpritesheetPath(codexHome, pet.SpritesheetFile) {
		t.Fatalf("path = %q", path)
	}
	if err := validateSpritesheet(path); err != nil {
		t.Fatalf("installed spritesheet invalid: %v", err)
	}

	path, err = EnsureBuiltinPet(codexHome, pet, fetch)
	if err != nil {
		t.Fatalf("cached ensure: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("fetches after cache = %d, want 1 (cached)", fetches)
	}
}

func TestEnsureBuiltinPetRejectsInvalidSpritesheet(t *testing.T) {
	codexHome := t.TempDir()
	pet, _ := BuiltinPet("dewey")
	fetch := func(url string, maxBytes int64) ([]byte, error) {
		return testSpritesheetPNG(t, 2, 2), nil
	}
	if _, err := EnsureBuiltinPet(codexHome, pet, fetch); err == nil {
		t.Fatal("expected invalid-dimension error")
	}
}

func TestEnsureBuiltinPetRejectsOversizedDownload(t *testing.T) {
	codexHome := t.TempDir()
	pet, _ := BuiltinPet("dewey")
	fetch := func(url string, maxBytes int64) ([]byte, error) {
		return bytes.Repeat([]byte{1}, int(maxBytes)+1), nil
	}
	if _, err := EnsureBuiltinPet(codexHome, pet, fetch); err == nil {
		t.Fatal("expected size error")
	}
}

func TestFrameCacheKey(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/sheet.webp"
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	first, err := FrameCacheKey(path, 192, 208, 8, 9)
	if err != nil {
		t.Fatalf("FrameCacheKey: %v", err)
	}
	second, err := FrameCacheKey(path, 192, 208, 8, 9)
	if err != nil {
		t.Fatalf("FrameCacheKey: %v", err)
	}
	if first != second {
		t.Fatalf("cache keys differ for identical file: %q vs %q", first, second)
	}
	if err := os.WriteFile(path, []byte("abd"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	changed, err := FrameCacheKey(path, 192, 208, 8, 9)
	if err != nil {
		t.Fatalf("FrameCacheKey: %v", err)
	}
	if first == changed {
		t.Fatalf("cache keys should change with contents")
	}
	if !bytes.Contains([]byte(first), []byte("sha256-")) {
		t.Fatalf("cache key missing sha256 prefix: %q", first)
	}
}
