package eventmap

import (
	"errors"
	"testing"
)

// Rust's ImagePreparationMode::UnifiedBudget ignores the image's detail hint and
// always prepares against the original-detail budget (6000 max dimension,
// 10000 patches); DetailBased keeps honouring auto/high/original and rejects
// low (image_preparation.rs resize_image).
func TestPrepareImagePrepUnifiedBudgetModeLikeRust(t *testing.T) {
	wide := pngDataURL(6401, 100)
	unified, err := PrepareImagePrepImageWithResultMode(wide, ImagePrepDetailHigh, ImagePrepModeUnifiedBudget)
	if err != nil {
		t.Fatalf("PrepareImagePrepImageWithResultMode(unified): %v", err)
	}
	if unified == nil || unified.Resize == nil || unified.Resize.PreparedWidth != 6000 || unified.Resize.PreparedHeight != 94 {
		t.Fatalf("unified wide resize = %#v, want 6000x94", unified)
	}

	// The mode overrides the hint, so a low-detail image is prepared, not rejected.
	low, err := PrepareImagePrepImageWithResultMode(pngDataURL(64, 32), ImagePrepDetailLow, ImagePrepModeUnifiedBudget)
	if err != nil || low == nil {
		t.Fatalf("unified low-detail image = %#v, %v", low, err)
	}

	if _, err := PrepareImagePrepImageWithResultMode(pngDataURL(64, 32), ImagePrepDetailLow, ImagePrepModeDetailBased); !errors.Is(err, ErrImagePrepUnsupportedLowDetail) {
		t.Fatalf("detail-based low-detail error = %v", err)
	}
	// DetailBased still uses the high-detail budget for a high hint.
	legacy, err := PrepareImagePrepImageWithResultMode(wide, ImagePrepDetailHigh, ImagePrepModeDetailBased)
	if err != nil || legacy == nil || legacy.Resize == nil || legacy.Resize.PreparedWidth != 2048 {
		t.Fatalf("detail-based wide resize = %#v, %v", legacy, err)
	}
}
