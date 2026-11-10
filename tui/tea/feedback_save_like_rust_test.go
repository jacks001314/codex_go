package tea

import (
	"errors"
	"strings"
	"testing"

	"codex_go/appserver"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
	"codex_go/utils"
)

func feedbackSaveTestModel(t *testing.T, uploadsEnabled bool, saveAvailable bool) (*Model, *[]appserver.FeedbackUploadParams, *[]appserver.FeedbackSaveParams) {
	t.Helper()
	uploads := &[]appserver.FeedbackUploadParams{}
	saves := &[]appserver.FeedbackSaveParams{}
	state := codextui.NewState(nil)
	options := Options{
		Width:           80,
		Height:          24,
		FeedbackEnabled: boolPtrTea(uploadsEnabled),
		OnSubmitFeedback: func(params appserver.FeedbackUploadParams) (appserver.FeedbackUploadResponse, error) {
			*uploads = append(*uploads, params)
			return appserver.FeedbackUploadResponse{ThreadID: "thread-upload"}, nil
		},
	}
	if saveAvailable {
		options.OnSaveFeedback = func(params appserver.FeedbackSaveParams) (appserver.FeedbackSaveResponse, error) {
			*saves = append(*saves, params)
			return appserver.FeedbackSaveResponse{SavedPath: params.SavePath, ThreadID: "thread-save"}, nil
		}
	}
	return NewModel(state, options), uploads, saves
}

func feedbackNoteModel(t *testing.T, uploadsEnabled bool, saveAvailable bool) (*Model, *[]appserver.FeedbackUploadParams, *[]appserver.FeedbackSaveParams) {
	t.Helper()
	model, uploads, saves := feedbackSaveTestModel(t, uploadsEnabled, saveAvailable)
	model.openFeedbackFlow()
	model.modal.feedback.category = feedbackCategoryBug
	model.openFeedbackConsent(model.modal.feedback)
	model.openFeedbackNote(model.modal.feedback)
	return model, uploads, saves
}

// TestFeedbackSavedZIPLikeRust covers Rust #60542: Ctrl+S in the report editor
// saves a local diagnostic ZIP and never uploads.
func TestFeedbackSavedZIPLikeRust(t *testing.T) {
	model, uploads, saves := feedbackNoteModel(t, true, true)
	model.modal.feedback.note = "the build broke"
	model.modal.feedback.includeLogs = true
	runTeaCmd(t, model, model.updateFeedbackNote(keymsg.KeyMsg{Type: keymsg.KeyCtrlS}))
	if len(*uploads) != 0 {
		t.Fatalf("local save uploaded a report: %#v", *uploads)
	}
	if len(*saves) != 1 {
		t.Fatalf("local saves = %d, want 1", len(*saves))
	}
	saved := (*saves)[0]
	if saved.Classification != "bug" || !saved.IncludeLogs || saved.Reason == nil || *saved.Reason != "the build broke" {
		t.Fatalf("save params = %#v", saved)
	}
	if model.modal != nil {
		t.Fatalf("note editor stayed open after a save")
	}
	view := utils.StripANSI(model.renderTranscriptOverlayCached())
	if !strings.Contains(view, "Feedback ZIP saved to") {
		t.Fatalf("saved receipt missing from history:\n%s", view)
	}
}

// TestFeedbackSaveFallsBackToErrorLikeRust reports a save failure as a save
// error, without claiming the feedback was uploaded.
func TestFeedbackSaveFallsBackToErrorLikeRust(t *testing.T) {
	model, uploads, _ := feedbackSaveTestModel(t, true, true)
	model.onSaveFeedback = func(appserver.FeedbackSaveParams) (appserver.FeedbackSaveResponse, error) {
		return appserver.FeedbackSaveResponse{}, errors.New("boom")
	}
	model.openFeedbackFlow()
	model.modal.feedback.category = feedbackCategoryBug
	model.openFeedbackNote(model.modal.feedback)
	runTeaCmd(t, model, model.updateFeedbackNote(keymsg.KeyMsg{Type: keymsg.KeyCtrlS}))
	if len(*uploads) != 0 {
		t.Fatalf("failed save uploaded a report: %#v", *uploads)
	}
	view := utils.StripANSI(model.renderTranscriptOverlayCached())
	if !strings.Contains(view, "Failed to save feedback ZIP") {
		t.Fatalf("save failure was not reported:\n%s", view)
	}
}

// TestFeedbackEnterNeedsUploadAccessLikeRust covers Rust #60542: Enter only
// sends when uploads are enabled, while Ctrl+S still saves locally.
func TestFeedbackEnterNeedsUploadAccessLikeRust(t *testing.T) {
	model, uploads, saves := feedbackNoteModel(t, false, true)
	runTeaCmd(t, model, model.updateFeedbackNote(keymsg.KeyMsg{Type: keymsg.KeyEnter}))
	if len(*uploads) != 0 || len(*saves) != 0 {
		t.Fatalf("Enter submitted while uploads are disabled: uploads=%d saves=%d", len(*uploads), len(*saves))
	}
	if model.modal == nil || model.modal.feedback == nil {
		t.Fatalf("Enter closed the editor while uploads are disabled")
	}
	runTeaCmd(t, model, model.updateFeedbackNote(keymsg.KeyMsg{Type: keymsg.KeyCtrlS}))
	if len(*saves) != 1 || len(*uploads) != 0 {
		t.Fatalf("Ctrl+S with uploads disabled: saves=%d uploads=%d", len(*saves), len(*uploads))
	}
}

// TestFeedbackFlowNeedsADestinationLikeRust stops before collecting a note when
// neither destination is available, and keeps offering the flow when only the
// local save is.
func TestFeedbackFlowNeedsADestinationLikeRust(t *testing.T) {
	disabled, _, _ := feedbackSaveTestModel(t, false, false)
	disabled.openFeedbackFlow()
	if disabled.modal == nil || disabled.modal.feedback == nil || disabled.modal.feedback.stage != feedbackStageDisabled {
		t.Fatalf("no-destination flow did not stop at the disabled popup")
	}

	localOnly, _, _ := feedbackSaveTestModel(t, false, true)
	localOnly.openFeedbackFlow()
	if localOnly.modal == nil || localOnly.modal.feedback == nil || localOnly.modal.feedback.stage != feedbackStageCategory {
		t.Fatalf("local-only flow did not offer the report editor")
	}
}

// TestFeedbackNoteDisclosuresLikeRust covers the copy and hints Rust #60542
// shows for each destination.
func TestFeedbackNoteDisclosuresLikeRust(t *testing.T) {
	both, _, _ := feedbackNoteModel(t, true, true)
	body, title := modelFeedbackConsentCopy(both)
	if title != "Include logs and session history?" {
		t.Fatalf("consent title = %q, want the shared destination copy", title)
	}
	if !strings.Contains(body, "Saving a ZIP does not submit feedback to OpenAI.") {
		t.Fatalf("consent copy does not cover the local destination: %q", body)
	}
	note := both.renderFeedbackNoteModal()
	if !strings.Contains(note, "Ctrl+S save ZIP") || !strings.Contains(note, "Enter send to OpenAI") {
		t.Fatalf("note hints missing destinations:\n%s", note)
	}

	localOnly, _, _ := feedbackNoteModel(t, false, true)
	note = localOnly.renderFeedbackNoteModal()
	if !strings.Contains(note, "Feedback uploads are disabled.") {
		t.Fatalf("note editor did not explain disabled uploads:\n%s", note)
	}
	if strings.Contains(note, "Enter send to OpenAI") || !strings.Contains(note, "Ctrl+S save ZIP") {
		t.Fatalf("note hints do not match the available destination:\n%s", note)
	}
	if !strings.Contains(note, "which is not sent to OpenAI") {
		t.Fatalf("note editor did not disclose the local ZIP:\n%s", note)
	}
}

func modelFeedbackConsentCopy(model *Model) (string, string) {
	model.openFeedbackConsent(model.modal.feedback)
	return model.modal.body, model.modal.title
}
