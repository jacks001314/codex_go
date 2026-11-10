package tea

import (
	"bytes"
	"image"
	"image/png"

	"codex_go/tui/imagedecode"
)

// macOS pasteboard representation types Core reads (Rust #60455).
const (
	pasteboardTypePNG  = "public.png"
	pasteboardTypeJPEG = "public.jpeg"
	pasteboardTypeTIFF = "public.tiff"
)

// pasteboardImageItem is one pasteboard item: the representation types in the
// owner's advertised order, with encoded bytes for the image types Core may
// decode.
type pasteboardImageItem struct {
	Types []string
	Data  map[string][]byte
}

// firstPasteboardImage mirrors Rust's clipboard_paste::macos::first_image
// (#60455). PNG wins within an item even when the owner advertises it after
// other representations; JPEG and TIFF otherwise keep the owner's order, and a
// TIFF representation hands the paste to the TIFF reader. The third result
// reports that hand-off.
func firstPasteboardImage(items []pasteboardImageItem, decode func([]byte) (image.Image, error)) (image.Image, bool, bool) {
	if decode == nil {
		decode = func(data []byte) (image.Image, error) {
			return imagedecode.DecodeBytes(data, 0)
		}
	}
	for _, item := range items {
		if pasteboardItemHasType(item, pasteboardTypePNG) {
			if img, ok := decodePasteboardType(item, pasteboardTypePNG, decode); ok {
				return img, true, false
			}
		}
		for _, typ := range item.Types {
			switch typ {
			case pasteboardTypeTIFF:
				// Rust stops at the first TIFF so arboard reads it instead of
				// generating a second representation.
				return nil, false, true
			case pasteboardTypeJPEG:
				if img, ok := decodePasteboardType(item, pasteboardTypeJPEG, decode); ok {
					return img, true, false
				}
			}
		}
	}
	return nil, false, false
}

func pasteboardItemHasType(item pasteboardImageItem, typ string) bool {
	for _, offered := range item.Types {
		if offered == typ {
			return true
		}
	}
	return false
}

func decodePasteboardType(item pasteboardImageItem, typ string, decode func([]byte) (image.Image, error)) (image.Image, bool) {
	if item.Data == nil {
		return nil, false
	}
	data, ok := item.Data[typ]
	if !ok || len(data) == 0 {
		return nil, false
	}
	img, err := decode(data)
	if err != nil || img == nil {
		return nil, false
	}
	return img, true
}

// clipboardPNGPassthrough reports whether a clipboard PNG payload can reach the
// widget byte for byte. Rust #60455 decodes every clipboard image so EXIF
// orientation is applied, which Go only needs when a PNG actually carries an
// orientation tag.
func clipboardPNGPassthrough(data []byte) bool {
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) {
		return false
	}
	if _, err := png.DecodeConfig(bytes.NewReader(data)); err != nil {
		return false
	}
	return imagedecode.Orientation(data) == 1
}
