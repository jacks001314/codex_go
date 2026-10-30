package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	"codex_go/utils"
)

// Mirrors Rust #58202: a resume rejected for a provider mismatch falls back to a
// read-only history snapshot that renders the provider notice and offers c/n/Esc
// recoveries.
func TestProviderErrorResumeShowsNoticeAndBlocksInputLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a", Title: "Launch readiness", Path: "/saved/thread-a.jsonl"},
		Messages: []codextui.Message{{Role: codextui.RoleAssistant, Text: "history"}},
		ReadOnly: true,
		ProviderError: &appserver.ModelProviderError{
			Kind:                 appserver.ModelProviderMismatch,
			ModelProvider:        "old-provider",
			CurrentModelProvider: "new-provider",
		},
	})
	if !model.readOnlyThread || !model.hasProviderError() {
		t.Fatalf("provider error resume must be read-only with provider error, got readonly=%v err=%v", model.readOnlyThread, model.hasProviderError())
	}
	view := utils.StripANSI(model.View())
	for _, want := range []string{
		"Continue in a new chat",
		"This chat uses old-provider, but Codex now uses new-provider, so this chat is read-only.",
		"c continue in new chat",
		"n start fresh",
		"Esc other chats",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("provider notice missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "This conversation is open in another app") {
		t.Fatalf("provider notice must replace the external-writer notice:\n%s", view)
	}

	// Typing stays blocked and the continuation keys are not typed into a draft.
	model.Update(keyRunes('h'))
	model.Update(keyRunes('x'))
	if got := model.composerText(); got != "" {
		t.Fatalf("provider error must block direct input, composer = %q", got)
	}

	// `c` starts a new chat and drafts an unsent reference to the saved chat.
	model.Update(keyRunes('c'))
	if model.hasProviderError() || model.readOnlyThread {
		t.Fatal("continue must leave the provider-error state")
	}
	if got := strings.TrimSpace(model.State.ThreadID); got == "thread-a" {
		t.Fatalf("continue must leave the blocked thread, got %q", got)
	}
	wantDraft := "Read \u201cLaunch readiness\u201d and let\u2019s continue where we left off.\n\nSaved conversation: /saved/thread-a.jsonl"
	if got := model.composerText(); got != wantDraft {
		t.Fatalf("continuation draft = %q, want %q", got, wantDraft)
	}
}

// Mirrors Rust #58202: `n` starts a fresh session and `Esc` opens the resume
// picker so the user can pick another chat.
func TestProviderErrorFreshAndOtherChatsKeysLikeRust(t *testing.T) {
	fresh := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	fresh.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a", Title: "Saved"},
		ReadOnly: true,
		ProviderError: &appserver.ModelProviderError{
			Kind:          appserver.ModelProviderNotConfigured,
			ModelProvider: "retired-provider",
		},
	})
	if !strings.Contains(utils.StripANSI(fresh.View()), "Model provider unavailable") {
		t.Fatalf("missing not-configured title:\n%s", fresh.View())
	}
	fresh.Update(keyRunes('n'))
	if fresh.hasProviderError() || fresh.readOnlyThread {
		t.Fatal("n must leave the provider-error state")
	}
	if got := strings.TrimSpace(fresh.State.ThreadID); got == "thread-a" {
		t.Fatalf("n must start a different thread, got %q", got)
	}

	escape := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	escape.sessionItems = []codextui.SessionSummary{{ThreadID: "thread-b", Title: "Other chat"}}
	escape.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-a"},
		ReadOnly: true,
		ProviderError: &appserver.ModelProviderError{
			Kind:                  appserver.ModelProviderPolicyConflict,
			ModelProvider:         "old-provider",
			RequiredModelProvider: "required-provider",
		},
	})
	escape.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if escape.modal == nil || escape.modal.sessionPicker == nil {
		t.Fatal("Esc must open the resume picker")
	}
}

// Mirrors Rust #58202: switching to an agent thread blocked by a provider
// admission failure shows the same notice.
func TestAgentSwitchProviderErrorNoticeLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-b",
		Response: AgentThreadSwitchResponse{
			Entry:         codextui.AgentThreadEntry{ThreadID: "thread-b"},
			Status:        "idle",
			ReadOnly:      true,
			RolloutPath:   "/saved/thread-b.jsonl",
			ProviderError: &appserver.ModelProviderError{Kind: appserver.ModelProviderNotConfigured, ModelProvider: "retired"},
		},
	})
	if !model.hasProviderError() {
		t.Fatal("agent switch must show the provider error")
	}
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "Model provider unavailable") {
		t.Fatalf("agent switch provider notice missing:\n%s", view)
	}
}
