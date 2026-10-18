package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

func TestUserVerificationElicitationShowsExplicitDecisionLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	model.Update(ElicitationRequestMsg{
		ID:          "verification-1",
		ServerName:  "docs",
		Title:       "Verify identity",
		Mode:        "openai/userVerification",
		Description: "Confirm this request on your device.",
		Challenge:   "AQ",
	})
	if model.modal == nil || model.modal.kind != ModalKindElicitation {
		t.Fatalf("modal = %#v, want user verification elicitation", model.modal)
	}
	if !strings.Contains(model.modal.body, "Confirm this request on your device.") {
		t.Fatalf("body = %q", model.modal.body)
	}
	labels := make([]string, 0, len(model.modal.options))
	for _, option := range model.modal.options {
		labels = append(labels, option.Label)
	}
	if got := strings.Join(labels, ","); got != "Verify,Decline,Cancel" {
		t.Fatalf("options = %q", got)
	}
}

// TestUserVerificationPromptLinksCompleteURLsLikeRust mirrors #51458: both the
// title and request details carry terminal hyperlinks with the full target.
func TestUserVerificationPromptLinksCompleteURLsLikeRust(t *testing.T) {
	titleURL := "https://example.com/production/release/candidate"
	bodyURL := "https://example.com/production/critical-release-candidate"
	model := NewModel(codextui.NewState(nil), Options{Width: 40, Height: 20})
	model.Update(ElicitationRequestMsg{
		ID:          "verification-1",
		ServerName:  "docs",
		Title:       "Approve " + titleURL,
		Mode:        "openai/userVerification",
		Description: "Deploy " + bodyURL,
		Challenge:   "AQ",
	})
	if model.modal == nil || !model.modal.userVerification {
		t.Fatalf("modal = %#v, want a user verification elicitation", model.modal)
	}
	rendered := model.renderModal()
	for _, target := range []string{titleURL, bodyURL} {
		if !strings.Contains(rendered, codextui.OSC8Hyperlink(target, target)) {
			t.Fatalf("verification prompt missing complete hyperlink %q:\n%q", target, rendered)
		}
	}
	visible := codextui.StripOSC8(rendered)
	for _, target := range []string{titleURL, bodyURL} {
		if !strings.Contains(visible, target) {
			t.Fatalf("verification prompt split %q:\n%s", target, visible)
		}
	}
}
