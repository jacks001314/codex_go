package tea

// Mirrors Rust #61919 transcript_view::layout: a generated image renders at the
// body width and left-aligns, while an inspected file keeps the centered,
// bounded preview.

import (
	"testing"

	historycell "codex_go/tui/history_cell"
	"codex_go/tui/pets"
	"codex_go/tui/transcript_image"
)

func TestGeneratedPreviewSpansBodyAndLeftAlignsLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 1600, 800)
	model := transcriptImageModel(t, historycell.NewImageGenerationCall("img-1", "completed", "a wide banner", path))
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("image plans = %#v", model.transcriptImagePlans)
	}
	plan := model.transcriptImagePlans[0]
	if !plan.leftAligned {
		t.Fatal("generated preview was not left-aligned")
	}
	// An 80-column transcript leaves 78 body columns; the 2:1 aspect keeps the
	// 768px cap out of the way, so the body width is what the preview fills.
	if plan.columns != 78 {
		t.Fatalf("generated columns = %d, want the 78-column body", plan.columns)
	}
	if plan.columns <= 48 {
		t.Fatalf("generated preview kept the inspected 48-column budget: %d", plan.columns)
	}

	// The same file inspected through the bounded path still fits the 48-column,
	// six-row budget, which is what the generated kind has to escape.
	var cache transcriptImageCache
	inspected := cache.load(path, 80, pets.ImageProtocolKitty, transcript_image.CellSize{Width: 8, Height: 16}, false, false)
	if inspected == nil {
		t.Fatal("inspected preview failed to load")
	}
	if inspected.Columns > 48 || inspected.Rows > 6 {
		t.Fatalf("inspected cells = %dx%d, want at most 48x6", inspected.Columns, inspected.Rows)
	}
	if plan.columns <= int(inspected.Columns) {
		t.Fatalf("generated columns %d did not exceed inspected %d", plan.columns, inspected.Columns)
	}
}

func TestViewedImagePreviewStaysCenteredLikeRust(t *testing.T) {
	path := writeTranscriptTestPNG(t, 800, 800)
	model := transcriptImageModel(t, historycell.NewViewImageToolCallWithID("view-1", path, ""))
	model.focusTranscriptActivity()
	if !model.setTranscriptActivityExpanded(true) {
		t.Fatal("viewed image was not expandable")
	}
	if len(model.transcriptImagePlans) != 1 {
		t.Fatalf("image plans = %#v", model.transcriptImagePlans)
	}
	if model.transcriptImagePlans[0].leftAligned {
		t.Fatal("an inspected preview was left-aligned")
	}
}
