package eventmap

import (
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"testing"

	"codex_go/attachmentstore"
	"codex_go/utils"
)

func sanitizerJPEGDataURL(t *testing.T) string {
	t.Helper()
	source := image.NewRGBA(image.Rect(0, 0, 9, 7))
	for y := 0; y < 7; y++ {
		for x := 0; x < 9; x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x * 20), G: uint8(y * 30), B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, source, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func TestSanitizerPrepRecordsReplaySourceLikeRust(t *testing.T) {
	sourceURL := sanitizerJPEGDataURL(t)
	items := []ImagePrepContentItem{{Kind: ImagePrepContentImage, ImageURL: sourceURL, Detail: ImagePrepDetailHigh}}
	prepared, _, replay, err := PrepareImagePrepContentWithNoticesStoreDecoder(attachmentstore.InlineStore{}, ImagePrepOrigin{ThreadID: "thread-1"}, items, utils.DecoderSanitizer)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(prepared) != 1 || prepared[0].ImageURL == sourceURL {
		t.Fatalf("prepared = %#v", prepared)
	}
	if len(replay) != 1 {
		t.Fatalf("replay sources = %#v", replay)
	}
	digest := sha1.Sum([]byte(prepared[0].ImageURL))
	if !bytes.Equal(replay[0].PreparedURLDigest, digest[:]) {
		t.Fatalf("digest = %x, want %x", replay[0].PreparedURLDigest, digest)
	}
	if replay[0].Detail != ImagePrepDetailHigh {
		t.Fatalf("detail = %q", replay[0].Detail)
	}
	if !strings.HasPrefix(replay[0].OriginalDataURL, "data:application/octet-stream;base64,") {
		t.Fatalf("original = %q", replay[0].OriginalDataURL)
	}
	_, sourcePayload, _ := strings.Cut(sourceURL, ",")
	if !strings.HasSuffix(replay[0].OriginalDataURL, sourcePayload) {
		t.Fatalf("original payload does not carry the source bytes")
	}
}

func TestLegacyPrepDoesNotRecordReplaySources(t *testing.T) {
	sourceURL := sanitizerJPEGDataURL(t)
	items := []ImagePrepContentItem{{Kind: ImagePrepContentImage, ImageURL: sourceURL, Detail: ImagePrepDetailHigh}}
	_, _, replay, err := PrepareImagePrepContentWithNoticesStoreDecoder(attachmentstore.InlineStore{}, ImagePrepOrigin{}, items, utils.DecoderLegacy)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(replay) != 0 {
		t.Fatalf("legacy replay sources = %#v", replay)
	}
}
