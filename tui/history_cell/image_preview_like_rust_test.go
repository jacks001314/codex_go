package historycell

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImageHistoryCellsExposeLocalPreviewPathsLikeRust(t *testing.T) {
	cwd := t.TempDir()
	viewed := NewViewImageToolCallWithID("call-1", "shots/diagram.png", cwd)
	if got, want := viewed.ImagePath(), filepath.Join(cwd, "shots", "diagram.png"); got != want {
		t.Fatalf("viewed ImagePath() = %q, want %q", got, want)
	}
	if ids := viewed.ActivityIDs(); len(ids) != 1 || ids[0] != "image-view:call-1" {
		t.Fatalf("viewed ActivityIDs() = %#v", ids)
	}
	if display := strings.Join(viewed.DisplayLines(80), "\n"); display != "\u2022 Viewed image diagram.png" {
		t.Fatalf("viewed display = %q", display)
	}
	if raw := strings.Join(viewed.RawLines(), "\n"); !strings.Contains(raw, viewed.Label) {
		t.Fatalf("viewed raw line lost the full path: %q", raw)
	}
	if transcript := strings.Join(viewed.TranscriptLines(80), "\n"); !strings.Contains(transcript, viewed.Label) {
		t.Fatalf("viewed transcript line lost the full path: %q", transcript)
	}

	generated := NewImageGenerationCall("img-1", "completed", "prompt", "artifacts/out.png")
	if generated.ImagePath() == "" || !filepath.IsAbs(generated.ImagePath()) {
		t.Fatalf("generated ImagePath() = %q", generated.ImagePath())
	}
	failed := NewImageGenerationCall("img-2", "failed", "prompt", "artifacts/out.png")
	if failed.ImagePath() != "" {
		t.Fatalf("failed generation ImagePath() = %q", failed.ImagePath())
	}
	if display := strings.Join(failed.DisplayLines(80), "\n"); !strings.Contains(display, "Saved to:") {
		t.Fatalf("failed generation lost saved path text:\n%s", display)
	}

	fileURL := NewImageGenerationCall("img-3", "completed", "prompt", "file:///tmp/out.png")
	if !strings.HasSuffix(filepath.ToSlash(fileURL.ImagePath()), "/tmp/out.png") {
		t.Fatalf("file URL ImagePath() = %q", fileURL.ImagePath())
	}
}
