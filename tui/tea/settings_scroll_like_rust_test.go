package tea

import (
	"errors"
	"strings"
	"testing"
)

// Rust #46910: opening a settings picker keeps the reader at the same
// transcript position instead of jumping back to the live tail.
func TestSettingsPickerPreservesTranscriptPositionLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	model.activityFollow = false
	model.transcript.SetYOffset(3)
	before := model.transcript.YOffset

	model.openThemePicker()

	if got := model.transcript.YOffset; got != before {
		t.Fatalf("opening the theme picker moved the transcript: %d -> %d", before, got)
	}
	if model.activityFollow {
		t.Fatal("opening the theme picker resumed following")
	}
}

// Rust #46910: a settings save failure is visibly reported, so it resumes
// following even if the reader had scrolled away from the tail.
func TestSettingsWriteResultResumesFollowingLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	model.activityFollow = false
	model.transcript.SetYOffset(3)
	model.pendingSettingsRequestID = 41

	model.Update(SettingsWriteResultMsg{
		RequestID: 41,
		Kind:      settingsWriteKindApprovalsReviewer,
		Err:       errors.New("config file is read-only"),
	})

	if !model.activityFollow || !model.transcript.AtBottom() {
		t.Fatalf("settings failure did not resume following: follow=%v offset=%d", model.activityFollow, model.transcript.YOffset)
	}
	if !strings.Contains(model.lastTranscriptContent, "Failed to save approvals reviewer") {
		t.Fatalf("settings failure was not added to the transcript: %q", model.lastTranscriptContent)
	}
}

// Model-selection and memory-reset outcomes also carry confirmations or errors
// and therefore resume following.
func TestModelAndMemoryResultsResumeFollowingLikeRust(t *testing.T) {
	model := scrollableTranscript(t)
	model.activityFollow = false
	model.transcript.SetYOffset(3)
	model.applyModelSelectionSync(ModelSelectionSyncMsg{PersistErr: errors.New("read-only config")})
	if !model.activityFollow || !model.transcript.AtBottom() {
		t.Fatalf("model-selection failure did not resume following: follow=%v offset=%d", model.activityFollow, model.transcript.YOffset)
	}

	model = scrollableTranscript(t)
	model.activityFollow = false
	model.transcript.SetYOffset(3)
	model.applyMemoryResetResult(MemoryResetResultMsg{Err: errors.New("unavailable")})
	if !model.activityFollow || !model.transcript.AtBottom() {
		t.Fatalf("memory-reset failure did not resume following: follow=%v offset=%d", model.activityFollow, model.transcript.YOffset)
	}
}
