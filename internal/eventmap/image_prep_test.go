package eventmap

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestPrepareResponseItemsReplacesUnsupportedImages(t *testing.T) {
	valid := dataURL("image/png", []byte("png"))
	items := []ImagePrepResponseItem{{
		Kind: ImagePrepResponseMessage,
		Content: []ImagePrepContentItem{
			{Kind: ImagePrepContentImage, ImageURL: valid, Detail: ImagePrepDetailHigh},
			{Kind: ImagePrepContentImage, ImageURL: "https://example.com/image.png", Detail: ImagePrepDetailHigh},
			{Kind: ImagePrepContentImage, ImageURL: valid, Detail: ImagePrepDetailLow},
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
	tooLarge := dataURL("image/png", make([]byte, imagePrepHighDetailMaxBytes+1))
	if _, err := PrepareImagePrepImage(tooLarge, ImagePrepDetailHigh); !errors.Is(err, ErrImagePrepTooLarge) {
		t.Fatalf("PrepareImagePrepImage(large high) error = %v, want ErrImagePrepTooLarge", err)
	}
	if _, err := PrepareImagePrepImage(tooLarge, ImagePrepDetailOriginal); err != nil {
		t.Fatalf("PrepareImagePrepImage(original) error = %v", err)
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
