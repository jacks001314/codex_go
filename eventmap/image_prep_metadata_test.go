package eventmap

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"codex_go/utils"
)

func imagePrepMetadataTestImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for x := 0; x < 64; x++ {
		for y := 0; y < 32; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 5), B: 40, A: 255})
		}
	}
	return img
}

// Mirrors Rust's image metadata tests: a re-encoded (resized) image keeps the
// source's RGB ICC profile and EXIF payload, and a non-RGB profile is dropped.
func TestImagePrepPreservesRGBICCAndExifLikeRust(t *testing.T) {
	rgbProfile := append([]byte("0123456789abcdef"), []byte("RGB ")...)
	exif := []byte{0x49, 0x49, 0x2a, 0x00, 0x08, 0x00, 0x00, 0x00, 0x01, 0x00}

	for _, testCase := range []struct {
		name      string
		mediaType string
		encode    func(*image.RGBA) []byte
	}{
		{
			name:      "png",
			mediaType: "image/png",
			encode: func(img *image.RGBA) []byte {
				var buf bytes.Buffer
				if err := png.Encode(&buf, img); err != nil {
					t.Fatal(err)
				}
				return utils.ApplyPromptImageMetadataToContainer("image/png", buf.Bytes(), utils.PromptImageSourceMetadata{
					ICCProfile: rgbProfile,
					EXIF:       exif,
				})
			},
		},
		{
			name:      "jpeg",
			mediaType: "image/jpeg",
			encode: func(img *image.RGBA) []byte {
				var buf bytes.Buffer
				if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
					t.Fatal(err)
				}
				return utils.ApplyPromptImageMetadataToContainer("image/jpeg", buf.Bytes(), utils.PromptImageSourceMetadata{
					ICCProfile: rgbProfile,
					EXIF:       exif,
				})
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := testCase.encode(imagePrepMetadataTestImage())
			result, err := prepareImagePrepDecoded(source, PromptImageResizeLimits{MaxDimension: 32, MaxPatches: 100})
			if err != nil {
				t.Fatalf("prepareImagePrepDecoded() error = %v", err)
			}
			if result.Resize == nil {
				t.Fatal("expected the image to be resized")
			}
			metadata := utils.ExtractPromptImageSourceMetadata(mustBase64Payload(t, result.URL))
			if !bytes.Equal(metadata.ICCProfile, rgbProfile) {
				t.Fatalf("ICC profile = %q, want the source profile", metadata.ICCProfile)
			}
			if !bytes.Equal(metadata.EXIF, exif) {
				t.Fatalf("EXIF = %v, want the source payload", metadata.EXIF)
			}
		})
	}

	// A CMYK profile is not safe across re-encoding paths (Rust's RGB filter).
	cmykSource := func() []byte {
		var buf bytes.Buffer
		if err := png.Encode(&buf, imagePrepMetadataTestImage()); err != nil {
			t.Fatal(err)
		}
		profile := append([]byte("0123456789abcdef"), []byte("CMYK")...)
		return utils.ApplyPromptImageMetadataToContainer("image/png", buf.Bytes(), utils.PromptImageSourceMetadata{ICCProfile: profile})
	}()
	if metadata := utils.ExtractPromptImageSourceMetadata(cmykSource); len(metadata.ICCProfile) != 0 {
		t.Fatalf("non-RGB profile was extracted: %q", metadata.ICCProfile)
	}
}
