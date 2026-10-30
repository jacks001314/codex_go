package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/utils"
)

func openPatchApprovalForDisclosure(t *testing.T, model *Model, id string) {
	t.Helper()
	model.openApprovalModal(ApprovalRequestMsg{
		ID:    id,
		Title: "Would you like to make the following edits?",
	})
	if model.modal == nil || model.modal.id != id {
		t.Fatalf("approval modal %q did not open", id)
	}
}

// Rust #58281: a collapsed patch remains inspectable while its approval prompt
// is open, and expanding it anchors the transcript instead of chasing new
// output. Closing that approval resumes following the latest output.
func TestPatchApprovalInlineDisclosureResumesFollowLikeRust(t *testing.T) {
	model := disclosureFixture(t, 2)
	if !model.activityFollow {
		t.Fatal("fixture must start following the latest output")
	}
	openPatchApprovalForDisclosure(t, model, "approval-1")

	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "detail line 01") {
		t.Fatalf("click did not expand the details:\n%s", view)
	}
	if model.activityFollow {
		t.Fatal("expanding during an approval must pause following")
	}
	if model.resumeFollowAfterApproval != "approval-1" {
		t.Fatalf("resume marker = %q, want approval-1", model.resumeFollowAfterApproval)
	}

	// Closing the approval prompt resumes the latest output.
	model.modal = nil
	model.Update(bubbletea.WindowSizeMsg{Width: 60, Height: 12})
	if model.resumeFollowAfterApproval != "" {
		t.Fatalf("resume marker survived the approval: %q", model.resumeFollowAfterApproval)
	}
	if !model.activityFollow || !model.transcript.AtBottom() {
		t.Fatal("closing the approval did not resume following the latest output")
	}
}

// Advancing to another patch approval also resumes following (Rust
// sync_patch_approval compares the current approval identity).
func TestPatchApprovalInlineDisclosureResumesWhenApprovalAdvancesLikeRust(t *testing.T) {
	model := disclosureFixture(t, 2)
	openPatchApprovalForDisclosure(t, model, "approval-1")
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	if model.resumeFollowAfterApproval != "approval-1" {
		t.Fatalf("resume marker = %q, want approval-1", model.resumeFollowAfterApproval)
	}

	openPatchApprovalForDisclosure(t, model, "approval-2")
	model.Update(bubbletea.WindowSizeMsg{Width: 60, Height: 12})
	if model.resumeFollowAfterApproval != "" || !model.activityFollow {
		t.Fatalf("advancing the approval did not resume following: marker=%q follow=%v",
			model.resumeFollowAfterApproval, model.activityFollow)
	}
}

// Manual scrolling keeps the reader where they moved and cancels the pending
// resume (Rust clears resume_after_approval in scroll()).
func TestPatchApprovalInlineDisclosureManualScrollCancelsResumeLikeRust(t *testing.T) {
	model := disclosureFixture(t, 20)
	openPatchApprovalForDisclosure(t, model, "approval-1")
	y, hit := transcriptDisclosureControlPoint(t, model)
	clickTranscriptRow(model, hit.x, y)
	if model.resumeFollowAfterApproval == "" {
		t.Fatal("click did not record the pending resume")
	}

	model.scrollTranscriptRows(-1)
	if model.resumeFollowAfterApproval != "" {
		t.Fatalf("manual scroll kept the resume marker: %q", model.resumeFollowAfterApproval)
	}
	if model.activityFollow || model.transcript.AtBottom() {
		t.Fatal("manual scroll did not move away from the latest output")
	}
	offset := model.transcript.YOffset
	model.modal = nil
	model.Update(bubbletea.WindowSizeMsg{Width: 60, Height: 12})
	if model.activityFollow {
		t.Fatal("closing the approval overrode the manual scroll")
	}
	if model.transcript.YOffset != offset {
		t.Fatalf("closing the approval moved the transcript: offset=%d, want %d", model.transcript.YOffset, offset)
	}
}
