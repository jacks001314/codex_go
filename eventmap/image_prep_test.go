package eventmap

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func TestPrepareResponseItemsReplacesUnsupportedImages(t *testing.T) {
	valid := pngDataURL(64, 32)
	items := []ImagePrepResponseItem{{
		Kind: ImagePrepResponseMessage,
		Content: []ImagePrepContentItem{
			{Kind: ImagePrepContentImage, ImageURL: valid, Detail: ImagePrepDetailHigh},
			{Kind: ImagePrepContentImage, ImageURL: "https://example.com/image.png", Detail: ImagePrepDetailHigh},
			{Kind: ImagePrepContentImage, ImageURL: valid, Detail: ImagePrepDetailLow},
			{Kind: ImagePrepContentImage, ImageURL: dataURL("image/png", []byte("not an image")), Detail: ImagePrepDetailHigh},
		},
	}}
	got := PrepareImagePrepResponseItems(items)
	if got[0].Content[0].Kind != ImagePrepContentImage || got[0].Content[0].ImageURL != valid {
		t.Fatalf("valid image changed: %#v", got[0].Content[0])
	}
	if got[0].Content[1].Text != ImagePrepRemoteURLPlaceholder {
		t.Fatalf("remote placeholder = %q", got[0].Content[1].Text)
	}
	if got[0].Content[2].Text != ImagePrepUnsupportedLowDetailPlaceholder {
		t.Fatalf("low detail placeholder = %q", got[0].Content[2].Text)
	}
	if got[0].Content[3].Text != ImagePrepProcessingErrorPlaceholder {
		t.Fatalf("invalid image placeholder = %q", got[0].Content[3].Text)
	}
}

func TestPrepareImagePrepResizesLargeImagesLikeRust(t *testing.T) {
	// Rust detail_policies_apply_the_expected_budgets: a 2048x2048 image with
	// high detail (2048 max dimension / 2500 patches) is resized to 1600x1600.
	large := pngDataURL(2048, 2048)
	result, err := PrepareImagePrepImageWithResult(large, ImagePrepDetailHigh)
	if err != nil {
		t.Fatalf("PrepareImagePrepImageWithResult: %v", err)
	}
	if result == nil || result.Resize == nil {
		t.Fatalf("expected resize, got %#v", result)
	}
	if result.Resize.SourceWidth != 2048 || result.Resize.SourceHeight != 2048 ||
		result.Resize.PreparedWidth != 1600 || result.Resize.PreparedHeight != 1600 {
		t.Fatalf("resize = %#v, want 2048x2048 -> 1600x1600", result.Resize)
	}
	decoded, _, width, height, err := decodeImagePrepBytes(mustBase64Payload(t, result.URL))
	if err != nil {
		t.Fatalf("decode prepared image: %v", err)
	}
	_ = decoded
	if width != 1600 || height != 1600 {
		t.Fatalf("prepared dimensions = %dx%d, want 1600x1600", width, height)
	}
}

func TestPrepareImagePrepPreservesSmallImageBytesLikeRust(t *testing.T) {
	// Rust preparation_preserves_small_image_bytes_and_replaces_remote_urls:
	// a 64x32 image under the budget is preserved byte-for-byte.
	small := pngDataURL(64, 32)
	result, err := PrepareImagePrepImageWithResult(small, ImagePrepDetailHigh)
	if err != nil {
		t.Fatalf("PrepareImagePrepImageWithResult: %v", err)
	}
	if result == nil || result.Resize != nil {
		t.Fatalf("small image should not resize: %#v", result)
	}
	if result.URL != small {
		t.Fatalf("small image bytes changed: got %q want %q", result.URL, small)
	}
}

func TestPrepareImagePrepOriginalUsesUnifiedLimitsLikeRust(t *testing.T) {
	// Rust: (6401,100) with original detail (6000 max / 10000 patches) ->
	// (6000,94); a 2048x2048 image with original detail stays 2048x2048
	// (fits 6000/10000).
	wide := pngDataURL(6401, 100)
	result, err := PrepareImagePrepImageWithResult(wide, ImagePrepDetailOriginal)
	if err != nil {
		t.Fatalf("PrepareImagePrepImageWithResult(original): %v", err)
	}
	if result == nil || result.Resize == nil ||
		result.Resize.PreparedWidth != 6000 || result.Resize.PreparedHeight != 94 {
		t.Fatalf("original wide resize = %#v, want 6000x94", result.Resize)
	}

	square := pngDataURL(2048, 2048)
	result, err = PrepareImagePrepImageWithResult(square, ImagePrepDetailOriginal)
	if err != nil {
		t.Fatalf("PrepareImagePrepImageWithResult(original): %v", err)
	}
	if result == nil || result.Resize != nil {
		t.Fatalf("2048x2048 with original detail should not resize: %#v", result)
	}
}

func TestPrepareImagePrepContentWithNoticesNumbering(t *testing.T) {
	small := pngDataURL(64, 32)
	large := pngDataURL(2048, 2048)
	items := []ImagePrepContentItem{
		{Kind: ImagePrepContentImage, ImageURL: small, Detail: ImagePrepDetailHigh},
		{Kind: ImagePrepContentImage, ImageURL: dataURL("image/png", []byte("%%%invalid%%%")), Detail: ImagePrepDetailHigh},
		{Kind: ImagePrepContentImage, ImageURL: large, Detail: ImagePrepDetailHigh},
	}
	prepared, resized, err := PrepareImagePrepContentWithNotices(items)
	if err != nil {
		t.Fatalf("PrepareImagePrepContentWithNotices: %v", err)
	}
	if len(prepared) != 3 {
		t.Fatalf("prepared count = %d, want 3", len(prepared))
	}
	if prepared[0].Kind != ImagePrepContentImage {
		t.Fatalf("first image changed: %#v", prepared[0])
	}
	if prepared[1].Kind != ImagePrepContentText || prepared[1].Text != ImagePrepProcessingErrorPlaceholder {
		t.Fatalf("failed image placeholder = %#v", prepared[1])
	}
	if len(resized) != 1 {
		t.Fatalf("resized count = %d, want 1 (third image)", len(resized))
	}
	// Rust: the resized image keeps its original position (image 3 of 3).
	if resized[0].ImageNumber != 3 || resized[0].ImageCount != 3 {
		t.Fatalf("resize numbering = %#v, want image 3 of 3", resized[0])
	}
}

func TestPrepareImageDataURLValidation(t *testing.T) {
	if _, err := PrepareImagePrepImage("data:image/png;base64,%%%", ImagePrepDetailHigh); !errors.Is(err, ErrImagePrepInvalidDataURL) {
		t.Fatalf("PrepareImagePrepImage(invalid base64) error = %v, want ErrImagePrepInvalidDataURL", err)
	}
	if _, err := PrepareImagePrepImage("data:image/png,abc", ImagePrepDetailHigh); !errors.Is(err, ErrImagePrepInvalidDataURL) {
		t.Fatalf("PrepareImagePrepImage(non base64) error = %v, want ErrImagePrepInvalidDataURL", err)
	}
}

func TestPrepareImageSizeLimits(t *testing.T) {
	// The byte-size guard runs before decode, so a payload over the limit is
	// rejected without attempting to parse it (matching the pre-existing Go
	// high/original byte budgets).
	highTooLarge := dataURL("image/png", make([]byte, imagePrepHighDetailMaxBytes+1))
	if _, err := PrepareImagePrepImage(highTooLarge, ImagePrepDetailHigh); !errors.Is(err, ErrImagePrepTooLarge) {
		t.Fatalf("PrepareImagePrepImage(large high) error = %v, want ErrImagePrepTooLarge", err)
	}
	originalTooLarge := dataURL("image/png", make([]byte, imagePrepOriginalDetailMaxBytes+1))
	if _, err := PrepareImagePrepImage(originalTooLarge, ImagePrepDetailOriginal); !errors.Is(err, ErrImagePrepTooLarge) {
		t.Fatalf("PrepareImagePrepImage(large original) error = %v, want ErrImagePrepTooLarge", err)
	}
	// A payload within the budget but not a decodable image surfaces the
	// processing-error placeholder through the caller, not a size error.
	if _, err := PrepareImagePrepImage(dataURL("image/png", []byte("not an image")), ImagePrepDetailOriginal); err == nil {
		t.Fatal("PrepareImagePrepImage(undecodable original) error = nil, want decode error")
	}
}

func TestPrepareImagePreservesNonDataLocalReferences(t *testing.T) {
	got, err := PrepareImagePrepImage("file:///tmp/image.png", ImagePrepDetailHigh)
	if err != nil {
		t.Fatalf("PrepareImagePrepImage(file) error = %v", err)
	}
	if got != "file:///tmp/image.png" {
		t.Fatalf("PrepareImagePrepImage(file) = %q", got)
	}
}

func TestURLPredicates(t *testing.T) {
	if !IsImagePrepRemoteURL("HTTPS://example.com/a.png") {
		t.Fatalf("IsImagePrepRemoteURL(HTTPS) = false")
	}
	if IsImagePrepRemoteURL("file:///a.png") {
		t.Fatalf("IsImagePrepRemoteURL(file) = true")
	}
	if !IsImagePrepDataURL("DATA:image/png;base64,abc") {
		t.Fatalf("IsImagePrepDataURL(DATA) = false")
	}
}

func TestPlaceholderForError(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrImagePrepRemoteURLUnsupported, ImagePrepRemoteURLPlaceholder},
		{ErrImagePrepUnsupportedLowDetail, ImagePrepUnsupportedLowDetailPlaceholder},
		{ErrImagePrepTooLarge, ImagePrepTooLargePlaceholder},
		{ErrImagePrepInvalidDataURL, ImagePrepProcessingErrorPlaceholder},
	}
	for _, tc := range cases {
		if got := ImagePrepPlaceholderForError(tc.err); got != tc.want {
			t.Fatalf("ImagePrepPlaceholderForError(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestOriginalImageDetailHelpers(t *testing.T) {
	if !CanRequestOriginalImagePrepDetail("gpt-5") {
		t.Fatalf("CanRequestOriginalImagePrepDetail(gpt-5) = false")
	}
	if CanRequestOriginalImagePrepDetail("gpt-3.5") {
		t.Fatalf("CanRequestOriginalImagePrepDetail(gpt-3.5) = true")
	}
	if got := SanitizeOriginalImagePrepDetail(ImagePrepDetailOriginal, "gpt-3.5"); got != ImagePrepDetailHigh {
		t.Fatalf("SanitizeOriginalImagePrepDetail() = %q, want high", got)
	}
	if got := SanitizeOriginalImagePrepDetail("", "gpt-5"); got != ImagePrepDetailAuto {
		t.Fatalf("SanitizeOriginalImagePrepDetail(empty) = %q, want auto", got)
	}
}

func dataURL(mediaType string, bytes []byte) string {
	var b strings.Builder
	b.WriteString("data:")
	b.WriteString(mediaType)
	b.WriteString(";base64,")
	b.WriteString(base64.StdEncoding.EncodeToString(bytes))
	return b.String()
}

func pngDataURL(width int, height int) string {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 30, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return dataURL("image/png", buf.Bytes())
}

func mustBase64Payload(t *testing.T, imageURL string) []byte {
	t.Helper()
	_, payload, err := splitImagePrepDataURL(imageURL)
	if err != nil {
		t.Fatalf("splitImagePrepDataURL: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	return decoded
}
