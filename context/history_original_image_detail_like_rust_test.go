package context

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"codex_go/eventmap"
)

// originalDetailImageDataURL encodes a solid PNG so the estimator can read real
// dimensions without any fixture file.
func originalDetailImageDataURL(t *testing.T, width int, height int) string {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	canvas.Set(width-1, height-1, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatalf("png.Encode(%dx%d) error = %v", width, height, err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

func originalDetailImageItemTokens(t *testing.T, imageURL string, fileID string, detail string) int64 {
	t.Helper()
	return EstimateItemTokens(&HistoryItem{Content: []eventmap.ContentItem{{
		Kind:     eventmap.ContentInputImage,
		ImageURL: imageURL,
		FileID:   fileID,
		Detail:   detail,
	}}})
}

// Mirrors Rust #54776 (context_manager/history.rs estimate_image_bytes /
// estimate_original_image_bytes): only `detail: "original"` measures the inline
// payload, charging ceil(w/32)*ceil(h/32) patches at four bytes per patch and
// capped at ORIGINAL_IMAGE_MAX_PATCHES. Every other reference, and any payload
// the estimator cannot read, keeps the fixed 7373-byte resized estimate.
func TestOriginalDetailImageTokensUseDimensionsLikeRust(t *testing.T) {
	small := originalDetailImageDataURL(t, 64, 64)
	large := originalDetailImageDataURL(t, 256, 256)
	unreadable := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("not an image"))

	// Exact estimator charges: 2x2 patches = 16 bytes, 8x8 patches = 256 bytes.
	if got := EstimateInlineImageBytes(small, "original"); got != 16 {
		t.Fatalf("EstimateInlineImageBytes(64x64, original) = %d, want 16", got)
	}
	if got := EstimateInlineImageBytes(large, "original"); got != 256 {
		t.Fatalf("EstimateInlineImageBytes(256x256, original) = %d, want 256", got)
	}
	if got := EstimateInlineImageBytes(small, "auto"); got != resizedImageBytesEstimate {
		t.Fatalf("EstimateInlineImageBytes(64x64, auto) = %d, want %d", got, resizedImageBytesEstimate)
	}
	if got := EstimateInlineImageBytes(small, ""); got != resizedImageBytesEstimate {
		t.Fatalf("EstimateInlineImageBytes(64x64, empty detail) = %d, want %d", got, resizedImageBytesEstimate)
	}
	if got := EstimateInlineImageBytes(unreadable, "original"); got != resizedImageBytesEstimate {
		t.Fatalf("EstimateInlineImageBytes(unreadable, original) = %d, want the %d byte fallback", got, resizedImageBytesEstimate)
	}
	if got := EstimateImageReferenceBytes("", "file-1", "original"); got != originalImageMaxPatches*4 {
		t.Fatalf("EstimateImageReferenceBytes(file, original) = %d, want %d", got, originalImageMaxPatches*4)
	}

	// The same account reaches EstimateItemTokens, which is what the history
	// budget actually charges.
	smallOriginal := originalDetailImageItemTokens(t, small, "", "original")
	smallAuto := originalDetailImageItemTokens(t, small, "", "auto")
	if smallOriginal >= smallAuto {
		t.Fatalf("tokens(small, original) = %d, tokens(small, auto) = %d, want original smaller",
			smallOriginal, smallAuto)
	}
	if delta := smallAuto - smallOriginal; delta < 1838 || delta > 1841 {
		t.Fatalf("tokens(small, auto) - tokens(small, original) = %d, want about %d",
			delta, (resizedImageBytesEstimate-16)/4)
	}
	largeOriginal := originalDetailImageItemTokens(t, large, "", "original")
	if delta := largeOriginal - smallOriginal; delta < 59 || delta > 61 {
		t.Fatalf("tokens(large, original) - tokens(small, original) = %d, want about 60", delta)
	}

	// A payload the estimator cannot read falls back to the fixed estimate, so
	// the two detail settings charge the same bytes.
	unreadableOriginal := originalDetailImageItemTokens(t, unreadable, "", "original")
	unreadableAuto := originalDetailImageItemTokens(t, unreadable, "", "auto")
	if delta := unreadableAuto - unreadableOriginal; delta < -1 || delta > 1 {
		t.Fatalf("tokens(unreadable) detail delta = %d, want the fixed estimate for both", delta)
	}

	// Uploaded-file references keep the maximum patch reservation for original
	// detail (the dimensions are not in the reference).
	fileOriginal := originalDetailImageItemTokens(t, "", "file-1", "original")
	fileAuto := originalDetailImageItemTokens(t, "", "file-1", "auto")
	if delta := fileOriginal - fileAuto; delta < 8_155 || delta > 8_158 {
		t.Fatalf("tokens(file, original) - tokens(file, auto) = %d, want about %d",
			delta, (originalImageMaxPatches*4-resizedImageBytesEstimate)/4)
	}
}
