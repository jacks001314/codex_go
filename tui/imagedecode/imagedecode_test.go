package imagedecode

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

// fixtureRaster is the 3x2 grayscale raster Rust's image_decode_tests uses.
func fixtureRaster() *image.Gray {
	source := image.NewGray(image.Rect(0, 0, 3, 2))
	copy(source.Pix, []byte{0, 40, 80, 120, 160, 200})
	return source
}

func exifOrientationPayload(orientation int) []byte {
	payload := []byte{'I', 'I', 0x2a, 0x00, 0x08, 0x00, 0x00, 0x00, 0x01, 0x00, 0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00}
	return append(payload, byte(orientation), 0, 0, 0)
}

func encodeFixture(t *testing.T, format string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buffer, fixtureRaster())
	case "jpeg":
		err = jpeg.Encode(&buffer, fixtureRaster(), &jpeg.Options{Quality: 100})
	default:
		t.Fatalf("unknown format %q", format)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", format, err)
	}
	return buffer.Bytes()
}

// withJPEGOrientation inserts an APP1 EXIF segment right after the SOI marker.
func withJPEGOrientation(t *testing.T, data []byte, orientation int) []byte {
	t.Helper()
	segment := append([]byte("Exif\x00\x00"), exifOrientationPayload(orientation)...)
	app1 := []byte{0xFF, 0xE1, 0x00, 0x00}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(segment)+2))
	out := append([]byte{}, data[:2]...)
	out = append(out, app1...)
	out = append(out, segment...)
	return append(out, data[2:]...)
}

// withPNGOrientation inserts an eXIf chunk after IHDR.
func withPNGOrientation(t *testing.T, data []byte, orientation int) []byte {
	t.Helper()
	ihdrLength := int(binary.BigEndian.Uint32(data[8:12]))
	insertAt := 8 + 8 + ihdrLength + 4
	payload := exifOrientationPayload(orientation)
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

// expectedSource maps a display pixel back to the undecoded raster, mirroring
// the image crate operations Rust applies for each orientation.
func expectedSource(orientation, width, height, x, y int) (int, int, int, int) {
	switch orientation {
	case 2:
		return width, height, width - 1 - x, y
	case 3:
		return width, height, width - 1 - x, height - 1 - y
	case 4:
		return width, height, x, height - 1 - y
	case 5:
		return height, width, y, x
	case 6:
		return height, width, y, height - 1 - x
	case 7:
		return height, width, width - 1 - y, height - 1 - x
	case 8:
		return height, width, width - 1 - y, x
	default:
		return width, height, x, y
	}
}

func TestDecodeHonorsEveryEXIFOrientationLikeRust(t *testing.T) {
	for _, format := range []string{"png", "jpeg"} {
		for _, orientation := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8} {
			data := encodeFixture(t, format)
			if orientation != 0 {
				if format == "png" {
					data = withPNGOrientation(t, data, orientation)
				} else {
					data = withJPEGOrientation(t, data, orientation)
				}
			}
			raw, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("%s orientation %d: decode raw: %v", format, orientation, err)
			}
			decoded, err := DecodeBytes(data, 0)
			if err != nil {
				t.Fatalf("%s orientation %d: DecodeBytes: %v", format, orientation, err)
			}
			bounds := raw.Bounds()
			width, height := bounds.Dx(), bounds.Dy()
			wantWidth, wantHeight, _, _ := expectedSource(orientation, width, height, 0, 0)
			if decoded.Bounds().Dx() != wantWidth || decoded.Bounds().Dy() != wantHeight {
				t.Fatalf("%s orientation %d: decoded size %dx%d, want %dx%d", format, orientation,
					decoded.Bounds().Dx(), decoded.Bounds().Dy(), wantWidth, wantHeight)
			}
			for y := 0; y < decoded.Bounds().Dy(); y++ {
				for x := 0; x < decoded.Bounds().Dx(); x++ {
					_, _, sourceX, sourceY := expectedSource(orientation, width, height, x, y)
					want, _, _, _ := raw.At(bounds.Min.X+sourceX, bounds.Min.Y+sourceY).RGBA()
					got, _, _, _ := decoded.At(x, y).RGBA()
					if got != want {
						t.Fatalf("%s orientation %d: pixel (%d,%d) = %d, want %d", format, orientation, x, y, got, want)
					}
				}
			}
		}
	}
}

func TestOrientationReadsPNGAndJPEGMetadata(t *testing.T) {
	if got := Orientation(encodeFixture(t, "png")); got != 1 {
		t.Fatalf("PNG without eXIf orientation = %d", got)
	}
	if got := Orientation(withPNGOrientation(t, encodeFixture(t, "png"), 6)); got != 6 {
		t.Fatalf("PNG eXIf orientation = %d", got)
	}
	if got := Orientation(withJPEGOrientation(t, encodeFixture(t, "jpeg"), 8)); got != 8 {
		t.Fatalf("JPEG Exif orientation = %d", got)
	}
	if got := Orientation([]byte("not an image")); got != 1 {
		t.Fatalf("non-image orientation = %d", got)
	}
}

func TestDecodeRejectsImagesLargerThanTheBudgetLikeRust(t *testing.T) {
	data := encodeFixture(t, "jpeg")
	if _, err := DecodeBytes(data, 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("budget error = %v, want ErrTooLarge", err)
	}
	if _, err := DecodeBytes(data, 6*4); err != nil {
		t.Fatalf("budget 24 rejected a six-pixel image: %v", err)
	}
}
