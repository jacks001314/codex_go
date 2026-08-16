package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/eventmap"
)

// TestRustImagePreparationDimensionSamplesRunInGo is the djalign dynamic-layer
// method-1 shared-fixture differential for image preparation dimension
// budgets: the samples below mirror the Rust
// core/src/image_preparation_tests.rs detail_policies_apply_the_expected_budgets
// table (single source of truth). Go computes the same target dimensions from
// the same input dimensions and detail level.
//
// The Rust side is pinned by name: the test first verifies the referenced
// #[test] fn still exists in core/src/image_preparation_tests.rs, so upstream
// removal or renames break the contract instead of silently drifting.
func TestRustImagePreparationDimensionSamplesRunInGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	source, err := os.ReadFile(filepath.Join(root, "core", "src", "image_preparation_tests.rs"))
	if err != nil {
		t.Fatalf("ReadFile(image_preparation_tests.rs) error = %v", err)
	}
	if !strings.Contains(string(source), "fn detail_policies_apply_the_expected_budgets()") {
		t.Fatal("Rust test fn detail_policies_apply_the_expected_budgets no longer exists in core/src/image_preparation_tests.rs; re-sync the shared fixture")
	}

	cases := []struct {
		id     string
		detail eventmap.ImagePrepDetail
		width  uint32
		height uint32
		wantW  uint32
		wantH  uint32
	}{
		{id: "high_2048x2048_to_1600x1600", detail: eventmap.ImagePrepDetailHigh, width: 2048, height: 2048, wantW: 1600, wantH: 1600},
		{id: "original_6401x100_to_6000x94", detail: eventmap.ImagePrepDetailOriginal, width: 6401, height: 100, wantW: 6000, wantH: 94},
		{id: "original_3201x3201_to_3200x3200", detail: eventmap.ImagePrepDetailOriginal, width: 3201, height: 3201, wantW: 3200, wantH: 3200},
		{id: "auto_2048x2048_to_1600x1600", detail: eventmap.ImagePrepDetailAuto, width: 2048, height: 2048, wantW: 1600, wantH: 1600},
		{id: "empty_2048x2048_to_1600x1600", detail: "", width: 2048, height: 2048, wantW: 1600, wantH: 1600},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			limits := eventmap.HighDetailLimits
			if tc.detail == eventmap.ImagePrepDetailOriginal {
				limits = eventmap.UnifiedImageLimits
			}
			gotW, gotH := eventmap.PromptImageOutputDimensionsForLimits(tc.width, tc.height, limits)
			if gotW != tc.wantW || gotH != tc.wantH {
				t.Fatalf("dimensions = %dx%d, want %dx%d (Rust detail_policies_apply_the_expected_budgets)", gotW, gotH, tc.wantW, tc.wantH)
			}
		})
	}
}
