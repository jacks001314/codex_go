package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

const misalignmentViolationFixture = `{"error":{"turnId":"turn-1","codexErrorInfo":"misalignmentPolicyViolation","misalignment":{"errorType":"unauthorized_data_transfer","detailedExplanation":"We detected unsafe behavior.","steer":{"message":"Continue safely."}}}}`

func TestMisalignmentReviewContinueLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-1"
	var submitted []SubmitRequest
	model := NewModel(state, Options{
		Width:  120,
		Height: 36,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	updated, cmd := model.Update(TurnCompletedMsg{Err: errors.New(misalignmentViolationFixture)})
	runTeaCmd(t, model, cmd)
	model = updated.(*Model)
	if !model.misalignmentPolicyStopped || model.misalignmentReview == nil {
		t.Fatalf("misalignment state = stopped=%v review=%#v", model.misalignmentPolicyStopped, model.misalignmentReview)
	}
	if model.modal == nil || model.modal.id != misalignmentPrecautionID {
		t.Fatalf("precaution modal = %#v", model.modal)
	}
	reviewIndex := modalOptionIndex(model.modal, "review")
	if reviewIndex < 0 {
		t.Fatalf("precaution options = %#v", model.modal.options)
	}
	model.modal.selected = reviewIndex
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != misalignmentReviewID {
		t.Fatalf("review modal = %#v", model.modal)
	}
	if !strings.Contains(model.modal.body, `"Continue safely."`) || !strings.Contains(model.modal.body, "We detected unsafe behavior.") {
		t.Fatalf("review body = %q", model.modal.body)
	}

	model.modal.selected = modalOptionIndex(model.modal, "continue")
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if model.modal == nil || model.modal.id != misalignmentReviewID {
		t.Fatal("first continue press should only arm the explicit confirmation")
	}
	_, cmd = model.Update(key(bubbletea.KeyEnter))
	runTeaCmd(t, model, cmd)
	if len(submitted) != 1 || submitted[0].Prompt != "Continue safely." {
		t.Fatalf("submitted = %#v", submitted)
	}
	if override := submitted[0].ResponsesAPIClientMetadata["misalignment_override"]; !strings.Contains(override, `"timestamp"`) {
		t.Fatalf("misalignment metadata = %#v", submitted[0].ResponsesAPIClientMetadata)
	}
	if model.misalignmentPolicyStopped || model.misalignmentReview != nil {
		t.Fatalf("continue did not clear misalignment state: stopped=%v review=%#v", model.misalignmentPolicyStopped, model.misalignmentReview)
	}
}

func TestMisalignmentReviewDisablesContinueWithoutSteerLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-1"
	model := NewModel(state, Options{Width: 120, Height: 36})
	errorMessage := `{"error":{"turnId":"turn-1","codexErrorInfo":"misalignmentPolicyViolation","misalignment":{"detailedExplanation":"No continuation available."}}}`
	updated, cmd := model.Update(TurnCompletedMsg{Err: errors.New(errorMessage)})
	runTeaCmd(t, model, cmd)
	model = updated.(*Model)
	model.modal.selected = modalOptionIndex(model.modal, "review")
	runTeaCmd(t, model, model.respondModal(false))
	continueIndex := modalOptionIndex(model.modal, "continue")
	if continueIndex < 0 || !model.modal.options[continueIndex].Disabled {
		t.Fatalf("continue option = %#v", model.modal.options)
	}
}

func TestMisalignmentClearsForDifferentNewTurnLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-1"
	model := NewModel(state, Options{Width: 120, Height: 36})
	updated, cmd := model.Update(TurnCompletedMsg{Err: errors.New(misalignmentViolationFixture)})
	runTeaCmd(t, model, cmd)
	model = updated.(*Model)
	model.applyThreadEvent(protocol.ThreadEvent{Type: "turn.started", ThreadID: "thread-1", TurnID: "turn-2"})
	if model.misalignmentPolicyStopped || model.misalignmentReview != nil {
		t.Fatalf("new turn did not clear stale review: stopped=%v review=%#v", model.misalignmentPolicyStopped, model.misalignmentReview)
	}
	if model.modal != nil && model.modal.kind == ModalKindMisalignment {
		t.Fatalf("misalignment modal survived a different turn: %#v", model.modal)
	}
}

func TestMisalignmentReviewRejectsStaleCurrentTurnLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-1"
	var submitted []SubmitRequest
	model := NewModel(state, Options{
		Width:  120,
		Height: 36,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
	})
	updated, cmd := model.Update(TurnCompletedMsg{Err: errors.New(misalignmentViolationFixture)})
	runTeaCmd(t, model, cmd)
	model = updated.(*Model)
	model.currentTurnID = "turn-2"
	if cmd := model.continueMisalignment(); cmd != nil || len(submitted) != 0 {
		t.Fatalf("stale review continued: cmd=%v submitted=%#v", cmd, submitted)
	}
	if !model.misalignmentPolicyStopped {
		t.Fatal("stale review cleared the stopped state")
	}
}

func TestMisalignmentPrecautionOffersRemoteAgentCommandCenterLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-1"
	model := NewModel(state, Options{Width: 120, Height: 36, RemoteAppServer: true})
	updated, cmd := model.Update(TurnCompletedMsg{Err: errors.New(misalignmentViolationFixture)})
	runTeaCmd(t, model, cmd)
	model = updated.(*Model)
	if modalOptionIndex(model.modal, "agents") < 0 {
		t.Fatalf("remote precaution options = %#v", model.modal.options)
	}
}

func TestMisalignmentReviewLongFindingsScrollLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.ThreadID = "thread-1"
	model := NewModel(state, Options{Width: 100, Height: 20})
	long := strings.Repeat("finding line\\n", 100)
	message := `{"error":{"turnId":"turn-1","codexErrorInfo":"misalignmentPolicyViolation","misalignment":{"detailedExplanation":"` + long + `","steer":{"message":"Continue safely."}}}}`
	updated, cmd := model.Update(TurnCompletedMsg{Err: errors.New(message)})
	runTeaCmd(t, model, cmd)
	model = updated.(*Model)
	model.modal.selected = modalOptionIndex(model.modal, "review")
	runTeaCmd(t, model, model.respondModal(false))
	if model.modal == nil || model.modal.id != misalignmentReviewID {
		t.Fatalf("review modal = %#v", model.modal)
	}
	if model.modal.bodyOffset != 0 {
		t.Fatalf("initial body offset = %d", model.modal.bodyOffset)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyPgDown})
	if model.modal.bodyOffset == 0 {
		t.Fatal("PageDown did not scroll the review body")
	}
	if view := model.renderModal(); !strings.Contains(view, "newer findings") {
		t.Fatalf("scroll indicator missing:\n%s", view)
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyPgUp})
	if model.modal.bodyOffset != 0 {
		t.Fatalf("PageUp did not return to the first page: offset=%d", model.modal.bodyOffset)
	}
}

func modalOptionIndex(modal *modalState, id string) int {
	if modal == nil {
		return -1
	}
	for index, option := range modal.options {
		if option.ID == id {
			return index
		}
	}
	return -1
}
