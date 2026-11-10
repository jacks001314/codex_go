package tea

import (
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func (m *Model) maybeOpenRateLimitSwitchPrompt(snapshot chatwidget.RateLimitSnapshot) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	currentModel := ""
	if m.State != nil {
		currentModel = m.State.Model
	}
	if chatwidget.ShouldQueueRateLimitSwitchPrompt(snapshot, currentModel, m.hideRateLimitModelNudge, m.rateLimitSwitchPrompt) {
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptPending
	}
	if m.hideRateLimitModelNudge {
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		return nil
	}
	// Rust's maybe_show_pending_rate_limit_prompt resets to idle on the Reserve
	// model, and leaves the prompt pending while an applicable backend banner
	// owns the account-recovery surface.
	if strings.EqualFold(strings.TrimSpace(currentModel), chatwidget.ReserveModelSlug) {
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		return nil
	}
	if m.backendBannerApplicable() {
		return nil
	}
	if m.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptPending {
		return nil
	}
	option, ok := m.lowerCostModelOption()
	if !ok {
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		return nil
	}
	m.openRateLimitSwitchPrompt(option)
	m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptShown
	return nil
}

// rateLimitSnapshotSource mirrors Rust's RateLimitSnapshotSource: thread
// events deliver rolling updates that only carry the windows they changed,
// while an account usage read returns the full windows for every limit and can
// therefore confirm that usage recovered (Rust #60435).
type rateLimitSnapshotSource int

const (
	rateLimitSnapshotRollingUpdate rateLimitSnapshotSource = iota
	rateLimitSnapshotAccountUsage
)

// rateLimitRecoveryTickMsg advances the recovery notice countdown.
type rateLimitRecoveryTickMsg struct{}

// startRateLimitRecoveryNotice replaces a visible switch prompt with the
// recovery notice and arms its five second deadline, matching the Recovering
// branch of Rust's apply_rate_limit_snapshot (#60435).
func (m *Model) startRateLimitRecoveryNotice() {
	if m == nil {
		return
	}
	m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptRecovering
	m.rateLimitRecoveryDeadline = m.currentTime().Add(
		time.Duration(chatwidget.RateLimitSwitchRecoveryNoticeSeconds) * time.Second,
	)
	m.rateLimitRecoverySecondsShown = 0
}

// tickRateLimitRecoveryPrompt mirrors Rust's tick_rate_limit_recovery_prompt:
// it rewrites the notice countdown once per second, dismisses the notice when
// the deadline passes, and otherwise schedules the next refresh. A notice whose
// view is gone leaves the prompt Shown so recovery does not re-arm it.
func (m *Model) tickRateLimitRecoveryPrompt(now time.Time) bubbletea.Cmd {
	if m == nil || m.rateLimitSwitchPrompt != chatwidget.RateLimitSwitchPromptRecovering {
		return nil
	}
	remaining := m.rateLimitRecoveryDeadline.Sub(now)
	if remaining <= 0 {
		m.closeRateLimitRecoveryNotice()
		return nil
	}
	seconds := int(remaining / time.Second)
	if remaining%time.Second != 0 {
		seconds++
	}
	if m.rateLimitRecoverySecondsShown != seconds {
		if m.modal == nil || m.modal.kind != ModalKindRateLimitSwitch {
			m.closeRateLimitRecoveryNotice()
			return nil
		}
		m.openSelectionViewModal(ModalKindRateLimitSwitch, chatwidget.NewRateLimitRecoveryNoticeView(seconds))
		m.rateLimitRecoverySecondsShown = seconds
	}
	delay := remaining - time.Duration(seconds-1)*time.Second
	if delay <= 0 {
		delay = time.Second
	}
	return bubbletea.Tick(delay, func(time.Time) bubbletea.Msg {
		return rateLimitRecoveryTickMsg{}
	})
}

// closeRateLimitRecoveryNotice dismisses the notice and keeps the prompt Shown,
// so a recovered account is not asked to switch models again. Both the
// countdown expiry and Enter/Esc land here (Rust #60435).
func (m *Model) closeRateLimitRecoveryNotice() {
	if m == nil {
		return
	}
	if m.modal != nil && m.modal.kind == ModalKindRateLimitSwitch {
		m.modal = nil
		m.refreshTranscript()
	}
	m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptShown
	m.rateLimitRecoveryDeadline = time.Time{}
	m.rateLimitRecoverySecondsShown = 0
}

func (m *Model) lowerCostModelOption() (codextui.ModelPickerOption, bool) {
	if m == nil {
		return codextui.ModelPickerOption{}, false
	}
	options := append([]codextui.ModelPickerOption(nil), m.modelPickerOpts...)
	if len(options) == 0 {
		options = codextui.BundledModelPickerOptions()
	}
	for _, option := range options {
		if strings.TrimSpace(option.ID) == chatwidget.NudgeModelSlug {
			return option, true
		}
	}
	return codextui.ModelPickerOption{}, false
}

func (m *Model) openRateLimitSwitchPrompt(option codextui.ModelPickerOption) {
	if m == nil {
		return
	}
	reasoning := option.DefaultReasoning()
	m.rateLimitSwitchModel = strings.TrimSpace(option.ID)
	m.rateLimitSwitchReasoning = strings.TrimSpace(reasoning)
	view := chatwidget.NewRateLimitSwitchPromptView(chatwidget.RateLimitSwitchPreset{
		Model:                  option.ID,
		Description:            option.Description,
		DefaultReasoningEffort: reasoning,
	})
	m.openSelectionViewModal(ModalKindRateLimitSwitch, view)
}

func (m *Model) applyRateLimitSwitchModalOption(optionID string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	switch strings.TrimSpace(optionID) {
	case "switch":
		return m.switchToRateLimitNudgeModel()
	case "hide":
		m.hideRateLimitModelNudge = true
		m.rateLimitSwitchPrompt = chatwidget.RateLimitSwitchPromptIdle
		m.notice = "Rate limit model switch reminders hidden."
		m.refreshTranscript()
		if m.onWriteSettings != nil {
			hidden := true
			return m.writeSettings(settingsWriteKindRateLimitModelNudge, []SettingsEdit{{
				KeyPath: "notices.hide_rate_limit_model_nudge",
				Value:   hidden,
			}})
		}
		return nil
	default:
		m.notice = "Keeping current model."
		m.refreshTranscript()
		return nil
	}
}

func (m *Model) switchToRateLimitNudgeModel() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	model := strings.TrimSpace(m.rateLimitSwitchModel)
	if model == "" {
		model = chatwidget.NudgeModelSlug
	}
	reasoning := strings.TrimSpace(m.rateLimitSwitchReasoning)
	if m.State != nil {
		m.State.Model = model
		if reasoning != "" {
			m.State.ReasoningEffort = reasoning
		}
		m.notice = strings.TrimSpace(m.State.RenderSetting("Model", m.State.Model))
		if reasoning != "" {
			m.notice += "\n" + strings.TrimSpace(m.State.RenderSetting("Reasoning", m.State.ReasoningEffort))
		}
	} else {
		m.notice = "Model: " + model
	}
	m.refreshTranscript()
	cmds := []bubbletea.Cmd{m.refreshStatusControlsCmd()}
	if m.onModalResponse != nil {
		effectiveReasoning := reasoning
		if m.State != nil {
			effectiveReasoning = m.State.ReasoningEffort
		}
		cmds = append(cmds, m.onModalResponse(ModalResponse{
			ID:       chatwidget.RateLimitSwitchPromptViewID,
			Kind:     ModalKindRateLimitSwitch,
			OptionID: "switch",
			Picker: &PickerDecision{
				Kind:            "rate_limit_switch_model",
				Value:           model,
				ReasoningEffort: effectiveReasoning,
			},
		}))
	}
	return bubbletea.Batch(cmds...)
}
