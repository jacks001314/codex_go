package tui

import (
	"strings"
	"testing"
	"time"
)

func TestSessionPickerRendersTranscriptPreviewInExpandedRow(t *testing.T) {
	picker := NewSessionPickerState(SessionPickerResume, []SessionSummary{{ThreadID: "thread-1", Preview: "hi"}}, "")
	now := time.Now()
	picker.Expanded["thread-1"] = true

	if joined := strings.Join(picker.RenderRows(60, now), "\n"); strings.Contains(joined, "Loading recent transcript") {
		t.Fatalf("no preview should render before it is requested:\n%s", joined)
	}

	if !picker.BeginTranscriptPreview("thread-1") {
		t.Fatal("the first expansion should request a preview")
	}
	if joined := strings.Join(picker.RenderRows(60, now), "\n"); !strings.Contains(joined, "  \u2502 Loading recent transcript...") {
		t.Fatalf("loading preview missing:\n%s", joined)
	}
	if picker.BeginTranscriptPreview("thread-1") {
		t.Fatal("a cached state must not be re-requested")
	}

	picker.SetTranscriptPreview("thread-1", TranscriptPreviewState{
		Kind: TranscriptPreviewLoadedState,
		Lines: []TranscriptPreviewLine{
			{Speaker: TranscriptPreviewUser, Text: "hello"},
			{Speaker: TranscriptPreviewAssistant, Text: "world"},
		},
	})
	joined := strings.Join(picker.RenderRows(60, now), "\n")
	if !strings.Contains(joined, "  \u2502 hello") || !strings.Contains(joined, "  \u2514 world") {
		t.Fatalf("loaded preview missing:\n%s", joined)
	}

	picker.SetTranscriptPreview("thread-1", TranscriptPreviewState{Kind: TranscriptPreviewLoadedState})
	if joined := strings.Join(picker.RenderRows(60, now), "\n"); !strings.Contains(joined, "  \u2514 No transcript preview available") {
		t.Fatalf("empty preview missing:\n%s", joined)
	}

	picker.SetTranscriptPreview("thread-1", TranscriptPreviewState{Kind: TranscriptPreviewFailedState})
	if joined := strings.Join(picker.RenderRows(60, now), "\n"); !strings.Contains(joined, "  \u2502 Could not load transcript preview") {
		t.Fatalf("failed preview missing:\n%s", joined)
	}
}

func TestSessionPickerDenseExpandedRowShowsPreview(t *testing.T) {
	picker := NewSessionPickerState(SessionPickerResume, []SessionSummary{{ThreadID: "thread-1", Preview: "hi"}}, "")
	picker.Density = SessionDensityDense
	picker.Expanded["thread-1"] = true
	picker.SetTranscriptPreview("thread-1", TranscriptPreviewState{
		Kind:  TranscriptPreviewLoadedState,
		Lines: []TranscriptPreviewLine{{Speaker: TranscriptPreviewUser, Text: "hello"}},
	})
	joined := strings.Join(picker.RenderRows(60, time.Now()), "\n")
	if !strings.Contains(joined, "  \u2514 hello") {
		t.Fatalf("dense expanded preview missing:\n%s", joined)
	}
}
