package tea

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"

	"codex_go/tui/imagedecode"
)

func pasteboardFixture(t *testing.T, width int) []byte {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, width, 1))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, source); err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return buffer.Bytes()
}

func pasteboardDecode(data []byte) (image.Image, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return img, nil
}

// Rust #60455: a TIFF representation before any later PNG hands the paste to
// the TIFF reader instead of decoding an image from a following item.
func TestFirstPasteboardImageStopsAtTIFFLikeRust(t *testing.T) {
	items := []pasteboardImageItem{
		{Types: []string{pasteboardTypeTIFF}, Data: map[string][]byte{}},
		{Types: []string{pasteboardTypePNG}, Data: map[string][]byte{pasteboardTypePNG: pasteboardFixture(t, 2)}},
	}
	img, found, tiff := firstPasteboardImage(items, pasteboardDecode)
	if found || img != nil {
		t.Fatalf("first image = %v, found = %v; want the TIFF hand-off", img, found)
	}
	if !tiff {
		t.Fatal("a leading TIFF representation must hand the paste to the TIFF reader")
	}
}

// Rust #60455: JPEG keeps the owner's order against a later PNG item.
func TestFirstPasteboardImageKeepsItemOrderLikeRust(t *testing.T) {
	items := []pasteboardImageItem{
		{Types: []string{"public.utf8-plain-text"}, Data: map[string][]byte{}},
		{Types: []string{pasteboardTypeJPEG}, Data: map[string][]byte{pasteboardTypeJPEG: pasteboardFixture(t, 3)}},
		{Types: []string{pasteboardTypePNG}, Data: map[string][]byte{pasteboardTypePNG: pasteboardFixture(t, 2)}},
	}
	img, found, tiff := firstPasteboardImage(items, pasteboardDecode)
	if !found || tiff || img == nil {
		t.Fatalf("found = %v, tiff = %v, image = %v", found, tiff, img)
	}
	if img.Bounds().Dx() != 3 {
		t.Fatalf("decoded width = %d, want the earlier JPEG item", img.Bounds().Dx())
	}
}

// Rust #60455: PNG wins inside an item even when it is advertised last, and a
// failed PNG decode falls through to the item's JPEG representation.
func TestFirstPasteboardImagePrefersPNGWithinAnItemLikeRust(t *testing.T) {
	items := []pasteboardImageItem{{
		Types: []string{pasteboardTypeJPEG, pasteboardTypeTIFF, pasteboardTypePNG},
		Data: map[string][]byte{
			pasteboardTypePNG:  pasteboardFixture(t, 5),
			pasteboardTypeJPEG: pasteboardFixture(t, 3),
		},
	}}
	img, found, tiff := firstPasteboardImage(items, pasteboardDecode)
	if !found || tiff || img.Bounds().Dx() != 5 {
		t.Fatalf("width = %v, found = %v, tiff = %v; want the PNG representation", img, found, tiff)
	}

	broken := []pasteboardImageItem{{
		Types: []string{pasteboardTypeJPEG, pasteboardTypePNG},
		Data: map[string][]byte{
			pasteboardTypePNG:  []byte("not a png"),
			pasteboardTypeJPEG: pasteboardFixture(t, 4),
		},
	}}
	img, found, tiff = firstPasteboardImage(broken, pasteboardDecode)
	if !found || tiff || img.Bounds().Dx() != 4 {
		t.Fatalf("failed PNG did not fall through to JPEG: width = %v, found = %v", img, found)
	}
}

func TestFirstPasteboardImageReportsNoImage(t *testing.T) {
	items := []pasteboardImageItem{{Types: []string{"public.utf8-plain-text"}}}
	if img, found, tiff := firstPasteboardImage(items, func([]byte) (image.Image, error) {
		return nil, errors.New("decode failure")
	}); img != nil || found || tiff {
		t.Fatalf("image = %v, found = %v, tiff = %v", img, found, tiff)
	}
}

// Rust #60455: only a PNG without orientation metadata may be handed to the
// widget unchanged; oriented payloads decode and re-encode like Rust.
func TestClipboardPNGPassthroughSkipsOrientedPayloads(t *testing.T) {
	plain := pasteboardFixture(t, 2)
	if !clipboardPNGPassthrough(plain) {
		t.Fatal("an unoriented PNG should pass through verbatim")
	}
	if clipboardPNGPassthrough([]byte("\x89PNG\r\n\x1a\nbroken")) {
		t.Fatal("a corrupt PNG must not pass through")
	}
	if clipboardPNGPassthrough([]byte("not a png")) {
		t.Fatal("non-PNG data must not pass through")
	}
	oriented := withClipboardPNGOrientation(t, plain, 6)
	if clipboardPNGPassthrough(oriented) {
		t.Fatal("an oriented PNG must decode and re-encode")
	}
	img, err := imagedecode.DecodeBytes(oriented, 0)
	if err != nil {
		t.Fatalf("decode oriented png: %v", err)
	}
	if img.Bounds().Dy() != 2 || img.Bounds().Dx() != 1 {
		t.Fatalf("oriented size = %dx%d, want 1x2", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

// withClipboardPNGOrientation inserts an eXIf chunk after IHDR.
func withClipboardPNGOrientation(t *testing.T, data []byte, orientation int) []byte {
	t.Helper()
	ihdrLength := int(binary.BigEndian.Uint32(data[8:12]))
	insertAt := 8 + 8 + ihdrLength + 4
	payload := []byte{'I', 'I', 0x2a, 0x00, 0x08, 0x00, 0x00, 0x00, 0x01, 0x00, 0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, byte(orientation), 0, 0, 0}
	chunk := make([]byte, 4)
	binary.BigEndian.PutUint32(chunk, uint32(len(payload)))
	chunk = append(chunk, []byte("eXIf")...)
	chunk = append(chunk, payload...)
	crc := crc32.NewIEEE()
	crc.Write([]byte("eXIf"))
	crc.Write(payload)
	sum := make([]byte, 4)
	binary.BigEndian.PutUint32(sum, crc.Sum32())
	chunk = append(chunk, sum...)
	out := append([]byte{}, data[:insertAt]...)
	out = append(out, chunk...)
	return append(out, data[insertAt:]...)
}
