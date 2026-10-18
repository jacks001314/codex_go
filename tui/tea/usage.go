package tea

import (
	"strconv"
	"strings"

	"github.com/google/uuid"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
)

const usageChatGPTLoginRequired = "Sign in with ChatGPT to use /usage."

func (m *Model) applyUsageCommand(args string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if !m.hasChatGPTAccount {
		m.notice = usageChatGPTLoginRequired
		m.addErrorHistoryMessage(m.notice)
		m.refreshTranscript()
		return nil
	}
	args = strings.TrimSpace(args)
	if args != "" {
		view, ok := chatwidget.ParseTokenActivityView(args)
		if !ok {
			m.notice = "Usage: /usage [daily|weekly|cumulative]"
			return nil
		}
		return m.openTokenActivity(view)
	}
	view := chatwidget.NewUsageMenuView(chatwidget.UsageMenuState{
		HasChatGPTAccount:                m.hasChatGPTAccount,
		AvailableRateLimitResetCredits:   cloneInt64PtrTea(m.availableRateLimitResetCredits),
		RefreshWhenKnownResetCreditsZero: true,
	})
	view.FooterHint = m.usageHintLine("open")
	if m.availableRateLimitResetCredits != nil && *m.availableRateLimitResetCredits == 0 {
		view.RefreshResetAvailability = true
	}
	m.openUsageSelectionView(view)
	if view.RefreshResetAvailability && m.onReadRateLimitResetCredits != nil {
		return m.requestRateLimitResetCredits(false)
	}
	return nil
}

func (m *Model) applyUsageModalOption(optionID string) bubbletea.Cmd {
	switch chatwidget.UsageMenuAction(optionID) {
	case chatwidget.UsageMenuActionShowTokenActivity:
		return m.openTokenActivity(chatwidget.TokenActivityDaily)
	case chatwidget.UsageMenuActionOpenRateLimitReset:
		return m.openRateLimitResetView()
	case chatwidget.UsageMenuActionConsumeRateLimitReset, chatwidget.UsageMenuActionRetryRateLimitReset:
		return m.consumeRateLimitResetCredit()
	case chatwidget.UsageMenuActionCloseRateLimitReset:
		m.notice = "Closed"
	default:
		m.notice = "Usage"
	}
	m.refreshTranscript()
	return nil
}

func (m *Model) openRateLimitResetView() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.availableRateLimitResetCredits != nil && *m.availableRateLimitResetCredits > 0 {
		confirmation := m.rateLimitResetConfirmationView(*m.availableRateLimitResetCredits)
		confirmation.FooterHint = m.usageHintLine("confirm")
		m.openUsageSelectionView(confirmation)
		return nil
	}
	if m.availableRateLimitResetCredits != nil {
		m.openUsageSelectionView(chatwidget.RateLimitResetMessageView("You don't have any usage limit resets available."))
		return nil
	}
	if !m.hasChatGPTAccount {
		m.openUsageSelectionView(chatwidget.RateLimitResetMessageView("No usage limit resets are available."))
		return nil
	}
	if m.onReadRateLimitResetCredits == nil {
		m.openUsageSelectionView(chatwidget.RateLimitResetMessageView("Couldn't load usage limit resets. Please try again."))
		return nil
	}
	m.openUsageSelectionView(chatwidget.RateLimitResetLoadingView())
	return m.requestRateLimitResetCredits(true)
}

func (m *Model) openUsageSelectionView(view chatwidget.SelectionView) {
	if m == nil {
		return
	}
	// Rust #46862: every usage surface uses the shared picker row layout, so
	// descriptions collapse on narrow terminals.
	view.ColumnWidth = bottompane.DefaultPickerRowColumns()
	view.DescriptionLayout = bottompane.NewHideWhenNarrowDescriptionLayout(24)
	if strings.TrimSpace(view.FooterHint) == "" && view.AllowCancel {
		view.FooterHint = chatwidget.PickerPopupHintLine
	}
	options := make([]ModalOption, 0, len(view.Items))
	for _, item := range view.Items {
		id := string(item.Action)
		if id == "" {
			id = item.Name
		}
		options = append(options, ModalOption{
			ID:          id,
			Label:       item.Name,
			Description: item.Description,
			Disabled:    item.Disabled,
		})
	}
	m.openModal(ModalRequestMsg{
		ID:                view.ViewID,
		Kind:              ModalKindUsage,
		Title:             view.Title,
		Body:              view.Subtitle,
		Options:           options,
		FooterHint:        view.FooterHint,
		FooterNote:        view.FooterNote,
		ColumnWidth:       view.ColumnWidth,
		DescriptionLayout: view.DescriptionLayout,
	})
	if m.modal != nil && view.InitialSelectedIndex >= 0 && view.InitialSelectedIndex < len(m.modal.options) {
		m.modal.selected = view.InitialSelectedIndex
		if m.modal.options[m.modal.selected].Disabled {
			m.moveModalSelection(1)
		}
	}
}

// usageHintLine mirrors Rust #46862 usage_hint_line: action-specific labels
// rendered with the active list keymap.
func (m *Model) usageHintLine(acceptLabel string) string {
	parts := make([]string, 0, 2)
	for _, hint := range []struct {
		action string
		label  string
	}{
		{action: "accept", label: acceptLabel},
		{action: "cancel", label: "back"},
	} {
		bindings, _, _ := codextui.ResolvedKeymapBindings(m.keymapConfig, "list", hint.action)
		if len(bindings) == 0 {
			continue
		}
		key := strings.ReplaceAll(strings.TrimSpace(bindings[0]), "-", "+")
		if key == "" {
			continue
		}
		parts = append(parts, key+" "+hint.label)
	}
	return strings.Join(parts, " \u00b7 ")
}

func (m *Model) openTokenActivity(view chatwidget.TokenActivityView) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	width := m.width
	if width < 20 {
		width = 20
	}
	lines := []string{"/usage " + strings.ToLower(view.Label())}
	if m.onReadTokenActivity == nil {
		lines = append(lines, chatwidget.TokenActivityLines(view, chatwidget.NewTokenActivityErrorState(), width)...)
		m.State.AddHistoryLines(lines, lines)
		m.notice = "Usage"
		m.refreshTranscript()
		return nil
	}
	lines = append(lines, chatwidget.TokenActivityLines(view, chatwidget.NewTokenActivityLoadingState(), width)...)
	m.State.AddHistoryLines(lines, lines)
	m.notice = "Usage"
	m.refreshTranscript()
	requestID := m.nextUsageRequest()
	m.pendingTokenActivityRequestID = requestID
	return func() bubbletea.Msg {
		response, err := m.onReadTokenActivity(view)
		return TokenActivityResultMsg{RequestID: requestID, View: view, Response: response, Err: err}
	}
}

func (m *Model) applyTokenActivityResult(msg TokenActivityResultMsg) {
	if m == nil || m.pendingTokenActivityRequestID != msg.RequestID {
		return
	}
	m.pendingTokenActivityRequestID = 0
	width := m.width
	if width < 20 {
		width = 20
	}
	state := chatwidget.NewTokenActivityLoadedState(msg.Response, m.currentTime())
	if msg.Err != nil {
		state = chatwidget.NewTokenActivityErrorState()
	}
	lines := chatwidget.TokenActivityLines(msg.View, state, width)
	m.State.AddHistoryLines(lines, lines)
	m.notice = "Usage"
	m.refreshTranscript()
}

func (m *Model) requestRateLimitResetCredits(openPopup bool) bubbletea.Cmd {
	if m == nil || m.onReadRateLimitResetCredits == nil {
		return nil
	}
	requestID := m.nextUsageRequest()
	return m.requestRateLimitResetCreditsWithID(requestID, openPopup, false)
}

func (m *Model) requestRateLimitResetCreditsWithID(requestID uint64, openPopup bool, postConsume bool) bubbletea.Cmd {
	if m == nil || m.onReadRateLimitResetCredits == nil {
		return nil
	}
	m.pendingRateLimitResetRequestID = requestID
	m.pendingRateLimitResetForPopup = openPopup
	m.pendingRateLimitResetPostConsume = postConsume
	return func() bubbletea.Msg {
		available, err := m.onReadRateLimitResetCredits()
		return RateLimitResetCreditsResultMsg{RequestID: requestID, AvailableCount: available, Err: err}
	}
}

func (m *Model) applyRateLimitResetCreditsResult(msg RateLimitResetCreditsResultMsg) {
	if m == nil || m.pendingRateLimitResetRequestID != msg.RequestID {
		return
	}
	openPopup := m.pendingRateLimitResetForPopup
	postConsume := m.pendingRateLimitResetPostConsume
	m.pendingRateLimitResetRequestID = 0
	m.pendingRateLimitResetForPopup = false
	m.pendingRateLimitResetPostConsume = false
	if msg.Err != nil {
		if openPopup {
			if postConsume {
				m.openUsageSelectionView(chatwidget.RateLimitResetMessageView("Usage reset."))
			} else {
				m.openUsageSelectionView(chatwidget.RateLimitResetMessageView("Couldn't load usage limit resets. Please try again."))
			}
		}
		return
	}
	m.availableRateLimitResetCredits = cloneInt64PtrTea(&msg.AvailableCount)
	if openPopup {
		if postConsume {
			m.openUsageSelectionView(chatwidget.RateLimitResetMessageView("Usage reset. You have " + strconv.FormatInt(msg.AvailableCount, 10) + " " + chatwidget.ResetLabel(msg.AvailableCount) + " left."))
		} else if msg.AvailableCount > 0 {
			m.openUsageSelectionView(m.rateLimitResetConfirmationView(msg.AvailableCount))
		} else {
			m.openUsageSelectionView(chatwidget.RateLimitResetMessageView("You don't have any usage limit resets available."))
		}
		return
	}
	if m.modal != nil && m.modal.kind == ModalKindUsage && m.modal.id == chatwidget.UsageMenuViewID {
		m.openUsageSelectionView(chatwidget.NewUsageMenuView(chatwidget.UsageMenuState{
			HasChatGPTAccount:              m.hasChatGPTAccount,
			AvailableRateLimitResetCredits: cloneInt64PtrTea(m.availableRateLimitResetCredits),
		}))
	}
}

func (m *Model) consumeRateLimitResetCredit() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if m.onConsumeRateLimitResetCredit == nil {
		result := chatwidget.RateLimitResetConsumeResultView("", true)
		m.openUsageSelectionView(result.View)
		m.refreshTranscript()
		return nil
	}
	idempotencyKey := uuid.NewString()
	m.openUsageSelectionView(chatwidget.RateLimitResetConsumingView())
	requestID := m.nextUsageRequest()
	m.pendingRateLimitResetRequestID = requestID
	m.pendingRateLimitResetForPopup = true
	m.pendingRateLimitResetPostConsume = false
	return func() bubbletea.Msg {
		outcome, err := m.onConsumeRateLimitResetCredit(idempotencyKey)
		return RateLimitResetConsumeResultMsg{RequestID: requestID, IdempotencyKey: idempotencyKey, Outcome: outcome, Err: err}
	}
}

func (m *Model) applyRateLimitResetConsumeResult(msg RateLimitResetConsumeResultMsg) bubbletea.Cmd {
	if m == nil || m.pendingRateLimitResetRequestID != msg.RequestID {
		return nil
	}
	result := chatwidget.RateLimitResetConsumeResultView(msg.Outcome, msg.Err != nil)
	if result.AvailableCredits != nil {
		m.availableRateLimitResetCredits = cloneInt64PtrTea(result.AvailableCredits)
	}
	m.openUsageSelectionView(result.View)
	if result.RefreshCreditsAfterReset {
		m.availableRateLimitResetCredits = nil
		if m.onReadRateLimitResetCredits != nil {
			return m.requestRateLimitResetCreditsWithID(msg.RequestID, true, true)
		}
	}
	m.pendingRateLimitResetRequestID = 0
	m.pendingRateLimitResetForPopup = false
	m.pendingRateLimitResetPostConsume = false
	m.refreshTranscript()
	return nil
}

func (m *Model) rateLimitResetConfirmationView(availableCount int64) chatwidget.SelectionView {
	if m == nil {
		return chatwidget.RateLimitResetConfirmationView(availableCount, false, "")
	}
	return chatwidget.RateLimitResetConfirmationView(
		availableCount,
		chatwidget.HasMonthlyRateLimitWindow(m.rateLimitSnapshots),
		m.chatGPTPlanType,
	)
}

func (m *Model) nextUsageRequest() uint64 {
	m.nextUsageRequestID++
	if m.nextUsageRequestID == 0 {
		m.nextUsageRequestID = 1
	}
	return m.nextUsageRequestID
}
