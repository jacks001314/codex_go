package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// TestNewSessionStartFailureRestoresComposedInputLikeRust covers Rust #42671:
// a failed replacement-thread start leaves the active thread in place, renders
// the error, and returns the submitted message and attachments to the
// composer instead of losing them in the transcript.
func TestNewSessionStartFailureRestoresComposedInputLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.State.SetThreadID("thread-active")
	model.State.AddMessage(codextui.RoleAssistant, "existing work")

	request := SubmitRequest{
		Prompt: "keep this message",
		Attachments: []bottompane.ComposerAttachment{
			{Kind: bottompane.AttachmentImage, Path: "/tmp/shot.png"},
		},
	}
	if cmd := model.submitRequest(request, false); cmd != nil {
		t.Fatalf("submitRequest command = %v, want nil without a submit handler", cmd)
	}
	if !strings.Contains(modelMessageText(model), "keep this message") {
		t.Fatalf("submit did not render the optimistic prompt: %q", modelMessageText(model))
	}

	model.Update(NewSessionStartFailedMsg{
		ErrorMessage: "Failed to start a fresh session through the app server: boom",
		Request:      request,
	})

	if got := model.State.ThreadID; got != "thread-active" {
		t.Fatalf("active thread = %q, want it preserved", got)
	}
	if got := model.composer.Value(); got != "keep this message" {
		t.Fatalf("composer = %q, want the composed message restored", got)
	}
	if len(model.attachments) != 1 || model.attachments[0].Path != "/tmp/shot.png" {
		t.Fatalf("attachments = %#v, want the image restored", model.attachments)
	}
	transcript := modelMessageText(model)
	if strings.Contains(transcript, "keep this message") {
		t.Fatalf("optimistic prompt still rendered: %q", transcript)
	}
	if !strings.Contains(transcript, "existing work") {
		t.Fatalf("active transcript lost: %q", transcript)
	}
	if !strings.Contains(transcript, "Failed to start a fresh session through the app server: boom") {
		t.Fatalf("error not rendered: %q", transcript)
	}
	if got := model.State.Status; got != "error" {
		t.Fatalf("status = %q, want error", got)
	}
}
