package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func safetyRecourseTestMetadata(domain, topic string) map[string]any {
	return map[string]any{
		"safety_recourse": map[string]any{
			"schema_version": 1,
			"domain":         domain,
			"source":         "user_request",
			"source_message": map[string]any{
				"id":   "msg-1",
				"kind": "ordinary_message",
				"role": "user",
			},
			"topic": topic,
			"suggestions": []any{
				map[string]any{"label": "Safer overview", "prompt_text": "Give me a safety overview."},
				map[string]any{"label": "General background", "prompt_text": "Explain the general background."},
				map[string]any{"label": "Defensive controls", "prompt_text": "Describe defensive controls."},
			},
		},
	}
}

func newSafetyRecourseModel(options Options) *Model {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-safety")
	state.SetStatus("running")
	options.Width = 120
	options.Height = 40
	model := NewModel(state, options)
	model.currentTurnID = "turn-safety"
	return model
}

func (m *Model) renderedHistoryText() string {
	if m == nil || m.State == nil {
		return ""
	}
	lines := make([]string, 0, len(m.State.Messages))
	for _, message := range m.State.Messages {
		lines = append(lines, message.Text)
	}
	return strings.Join(lines, "\n")
}

func TestModelSafetyRecourseDefersBlockedTurnAndExplainsIt(t *testing.T) {
	model := newSafetyRecourseModel(Options{})
	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: map[string]any{
			"safety_block": map[string]any{
				"schema_version": 1,
				"domain":         "bio",
				"status":         "loading",
			},
		},
	})
	if model.pendingSafetyRecourse == nil {
		t.Fatalf("loading metadata did not open a pending notice")
	}
	if notice := model.renderSafetyRecourseNotice(); !strings.Contains(notice, "This content can't be shown") {
		t.Fatalf("pending notice = %q", notice)
	}

	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: safetyRecourseTestMetadata("bio", "risky_biological_activities"),
	})
	pending := model.pendingSafetyRecourse
	if pending == nil || pending.Result == nil {
		t.Fatalf("pending recourse = %#v", pending)
	}
	if model.workingStatusHeader != chatwidget.SafetyRecourseStatusHeader {
		t.Fatalf("status header = %q", model.workingStatusHeader)
	}

	bioMessage := chatwidget.SafetyAccessBlockPrefix + " blocked"
	model.applyThreadEvent(protocol.ErrorEvent(bioMessage))
	if model.Transcript.lastTurnError != "" {
		t.Fatalf("deferred error rendered a generic cell: %q", model.Transcript.lastTurnError)
	}
	if pending.DeferredError != bioMessage {
		t.Fatalf("deferred error = %q", pending.DeferredError)
	}

	model.Update(TurnCompletedMsg{Err: errors.New(bioMessage)})
	rendered := model.renderedHistoryText()
	if !strings.Contains(rendered, "We take extra caution with requests involving") {
		t.Fatalf("recourse explanation missing from history:\n%s", rendered)
	}
	if model.modal == nil || model.modal.id != chatwidget.SafetyRecourseViewID {
		t.Fatalf("rewrite picker = %#v", model.modal)
	}
	if model.modal.title != safetyRecoursePickerTitle || model.modal.body != safetyRecoursePickerSubtitle {
		t.Fatalf("picker copy = %q / %q", model.modal.title, model.modal.body)
	}
	if len(model.modal.options) != 3 {
		t.Fatalf("picker options = %#v", model.modal.options)
	}
}

func TestModelSafetyRecourseKeepsGenericErrorForMismatchedPolicy(t *testing.T) {
	model := newSafetyRecourseModel(Options{})
	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: safetyRecourseTestMetadata("cyber", "risky_cybersecurity_activities"),
	})
	if model.pendingSafetyRecourse == nil || model.pendingSafetyRecourse.Result == nil {
		t.Fatalf("pending recourse = %#v", model.pendingSafetyRecourse)
	}
	model.Update(TurnCompletedMsg{Err: errors.New("ordinary transport failure")})
	if model.modal != nil && model.modal.id == chatwidget.SafetyRecourseViewID {
		t.Fatalf("mismatched error opened the rewrite picker")
	}
	if !strings.Contains(model.renderedHistoryText(), "ordinary transport failure") {
		t.Fatalf("generic error missing:\n%s", model.renderedHistoryText())
	}
	if model.pendingSafetyRecourse != nil {
		t.Fatalf("pending recourse survived the completed turn")
	}
}

func TestModelSafetyRecourseFallsBackToPlainBlockWhenExplanationNeverArrives(t *testing.T) {
	model := newSafetyRecourseModel(Options{})
	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: map[string]any{
			"safety_block": map[string]any{
				"schema_version": 1,
				"domain":         "bio",
				"status":         "loading",
			},
		},
	})
	bioMessage := chatwidget.SafetyAccessBlockPrefix + " blocked"
	model.applyThreadEvent(protocol.ErrorEvent(bioMessage))
	model.applyThreadEvent(protocol.TurnCompletedWithID(protocol.Usage{}, "turn-safety"))
	rendered := model.renderedHistoryText()
	if !strings.Contains(rendered, "Eligible researchers can apply for Trusted Access.") {
		t.Fatalf("plain access block missing:\n%s", rendered)
	}
	if model.modal != nil && model.modal.id == chatwidget.SafetyRecourseViewID {
		t.Fatalf("fallback opened the rewrite picker")
	}
	if model.pendingSafetyRecourse != nil {
		t.Fatalf("pending recourse survived the fallback")
	}
	if model.Transcript.lastTurnError != "" {
		t.Fatalf("fallback rendered a generic error cell: %q", model.Transcript.lastTurnError)
	}
}

func TestModelSafetyRecourseLocalFailureWithoutExplanationKeepsAccessBlock(t *testing.T) {
	model := newSafetyRecourseModel(Options{})
	bioMessage := chatwidget.SafetyAccessBlockPrefix + " blocked"
	model.Update(TurnCompletedMsg{Err: errors.New(bioMessage)})
	if !strings.Contains(model.renderedHistoryText(), "Eligible researchers can apply for Trusted Access.") {
		t.Fatalf("plain access block missing:\n%s", model.renderedHistoryText())
	}
	if model.Transcript.lastTurnError != "" {
		t.Fatalf("bio failure rendered a generic error cell: %q", model.Transcript.lastTurnError)
	}
}

// TestModelSafetyRecourseMismatchedDomainUsesBioBlockLikeRust mirrors the Rust
// safety_buffering test matrix ("cyber" pending, bio error): the generic
// access block is shown, the failed turn stays failed, and no recourse picker
// opens.
func TestModelSafetyRecourseMismatchedDomainUsesBioBlockLikeRust(t *testing.T) {
	model := newSafetyRecourseModel(Options{RemoteAppServer: true})
	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: safetyRecourseTestMetadata("cyber", "unauthorized_account_or_system_access"),
	})
	bioMessage := "This request has been flagged for possible biological risk."
	model.applyThreadEvent(protocol.ErrorEvent(bioMessage))
	if !strings.Contains(model.renderedHistoryText(), "Eligible researchers can apply for Trusted Access.") {
		t.Fatalf("plain bio block missing:\n%s", model.renderedHistoryText())
	}
	if model.modal != nil && model.modal.id == chatwidget.SafetyRecourseViewID {
		t.Fatalf("mismatched domain opened the rewrite picker")
	}
	model.applyThreadEvent(protocol.TurnCompletedWithStatus(protocol.Usage{}, "turn-safety", true))
	if !strings.EqualFold(strings.TrimSpace(model.State.Status), "error") {
		t.Fatalf("failed completion status = %q", model.State.Status)
	}
	// The app-server turn submission also delivers TurnCompletedMsg after the
	// notifications; it must not turn the failed turn into a success.
	model.Update(TurnCompletedMsg{ThreadID: "thread-safety", Failed: true})
	if !strings.EqualFold(strings.TrimSpace(model.State.Status), "error") {
		t.Fatalf("failed final completion status = %q", model.State.Status)
	}
	rendered := model.renderedHistoryText()
	if strings.Contains(rendered, "Worked for") {
		t.Fatalf("failed turn rendered a completion footer:\n%s", rendered)
	}
	if got := strings.Count(rendered, "Eligible researchers can apply for Trusted Access."); got != 1 {
		t.Fatalf("bio block rendered %d times:\n%s", got, rendered)
	}
}

func TestModelSafetyRecourseCyberPickerOffersDaybreak(t *testing.T) {
	daybreakUpdates := 0
	model := newSafetyRecourseModel(Options{
		HasChatGPTAccount: true,
		ModelPickerOptions: []codextui.ModelPickerOption{
			{ID: "gpt-test", Label: "Test", AvailableCyberPrograms: []string{"standard", "daybreak_blue"}},
		},
		OnUpdateDaybreak: func(threadID string, enabled bool) error {
			daybreakUpdates++
			return nil
		},
	})
	model.State.Model = "gpt-test"
	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: safetyRecourseTestMetadata("cyber", "software_vulnerabilities_or_security_weaknesses"),
	})
	model.Update(CyberPolicyErrorMsg{ThreadID: "thread-safety", TurnID: "turn-safety"})
	if model.modal == nil || model.modal.id != chatwidget.SafetyRecourseViewID {
		t.Fatalf("cyber recourse picker = %#v", model.modal)
	}
	last := model.modal.options[len(model.modal.options)-1]
	if last.ID != safetyRecourseEnableDaybreakID || last.Label != "Enable Daybreak" {
		t.Fatalf("daybreak option = %#v", last)
	}
	model.applySafetyRecourseModalOption(safetyRecourseEnableDaybreakID)
	if daybreakUpdates != 1 || !model.daybreakEnabled {
		t.Fatalf("daybreak updates = %d enabled = %v", daybreakUpdates, model.daybreakEnabled)
	}
}

func TestModelSafetyRecourseCyberPickerOmitsUnavailableDaybreak(t *testing.T) {
	model := newSafetyRecourseModel(Options{
		HasChatGPTAccount: true,
		ModelPickerOptions: []codextui.ModelPickerOption{
			{ID: "gpt-test", Label: "Test", AvailableCyberPrograms: []string{"standard"}},
		},
	})
	model.State.Model = "gpt-test"
	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: safetyRecourseTestMetadata("cyber", "software_vulnerabilities_or_security_weaknesses"),
	})
	model.Update(CyberPolicyErrorMsg{ThreadID: "thread-safety", TurnID: "turn-safety"})
	if model.modal == nil || model.modal.id != chatwidget.SafetyRecourseViewID {
		t.Fatalf("cyber recourse picker = %#v", model.modal)
	}
	for _, option := range model.modal.options {
		if option.ID == safetyRecourseEnableDaybreakID {
			t.Fatalf("picker offered Daybreak for a model without it: %#v", model.modal.options)
		}
	}
}

func TestModelSafetyRecourseQueuesInputWhilePending(t *testing.T) {
	submitted := 0
	model := newSafetyRecourseModel(Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted++
			return nil
		},
	})
	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: safetyRecourseTestMetadata("bio", "risky_biological_activities"),
	})
	model.composer.SetValue("try this instead")
	model.submitComposer()
	if submitted != 0 {
		t.Fatalf("input submitted while recourse was pending")
	}
	if len(model.queued) != 1 || model.queued[0].Request.Prompt != "try this instead" {
		t.Fatalf("queued = %#v", model.queued)
	}
}

func TestModelSafetyRecourseSelectedRewriteUsesVerifiedFork(t *testing.T) {
	var requested [][3]string
	var submitted []SubmitRequest
	model := newSafetyRecourseModel(Options{
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			submitted = append(submitted, request)
			return nil
		},
		OnContinueSafetyRecourse: func(threadID, turnID, prompt string) bubbletea.Cmd {
			requested = append(requested, [3]string{threadID, turnID, prompt})
			return nil
		},
	})
	model.Update(TurnModerationMetadataMsg{
		ThreadID: "thread-safety",
		TurnID:   "turn-safety",
		Metadata: safetyRecourseTestMetadata("bio", "risky_biological_activities"),
	})
	model.applyThreadEvent(protocol.ErrorEvent(chatwidget.SafetyAccessBlockPrefix + " blocked"))
	model.Update(TurnCompletedMsg{Err: errors.New(chatwidget.SafetyAccessBlockPrefix + " blocked")})
	if model.modal == nil || model.modal.id != chatwidget.SafetyRecourseViewID {
		t.Fatalf("rewrite picker = %#v", model.modal)
	}
	model.applySafetyRecourseModalOption(safetyRecourseRewriteActionPrefix + "1")
	if len(requested) != 1 || requested[0] != [3]string{"thread-safety", "turn-safety", "Explain the general background."} {
		t.Fatalf("continue request = %#v", requested)
	}

	model.Update(SafetyRecourseRewriteReadyMsg{
		SourceThreadID: "thread-safety",
		Prompt:         "Explain the general background.",
		Response: SessionResumeResponse{
			Summary:  &codextui.SessionSummary{ThreadID: "thread-fork", Title: "Fork"},
			Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "Prefix turn"}},
			Status:   "idle",
		},
	})
	if model.State.ThreadID != "thread-fork" {
		t.Fatalf("active thread = %q", model.State.ThreadID)
	}
	if len(submitted) != 1 || submitted[0].Prompt != "Explain the general background." ||
		submitted[0].TurnTrigger != safetyRecourseFollowUpTrigger {
		t.Fatalf("submitted = %#v", submitted)
	}
}

func TestModelSafetyRecourseStaleForkDoesNotSwitchThreads(t *testing.T) {
	model := newSafetyRecourseModel(Options{})
	model.Update(SafetyRecourseRewriteReadyMsg{
		SourceThreadID: "thread-other",
		Prompt:         "Explain the general background.",
		Response: SessionResumeResponse{
			Summary: &codextui.SessionSummary{ThreadID: "thread-fork"},
		},
	})
	if model.State.ThreadID != "thread-safety" {
		t.Fatalf("stale fork switched thread to %q", model.State.ThreadID)
	}
}
