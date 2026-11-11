package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// Rust #60152: `close_sidebar_tab` (default `leader w`) closes the focused
// sidebar tab. Closing the displayed side chat returns to its parent and
// removes the tab from the chooser; sibling tabs stay registered. Go keeps
// side chats in memory, so closing loses their history and asks first when
// work would be discarded.
const (
	sideTabCloseModalID = "side-tab-close"
	sideTabCloseOption  = "close"
	sideTabCloseCancel  = "cancel"
)

// closeSidebarTab closes the focused tab: the displayed side conversation, or
// the live tab selected while the chooser owns focus.
func (m *Model) closeSidebarTab() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	side := m.sidebarTabCloseTarget()
	if side == nil {
		return nil
	}
	if losses := m.sidebarTabCloseLosses(side); len(losses) > 0 {
		m.pendingSideTabCloseThreadID = strings.TrimSpace(side.SideThreadID)
		m.openModal(ModalRequestMsg{
			ID:    sideTabCloseModalID,
			Kind:  ModalKindGeneric,
			Title: "Close side chat?",
			Body:  "This will " + strings.Join(losses, " and ") + ". Close " + sidebarTabCloseTitle(side) + "?",
			Options: []ModalOption{
				{ID: sideTabCloseCancel, Label: "Cancel"},
				{ID: sideTabCloseOption, Label: "Close"},
			},
			FooterHint: "left/right choose - enter confirm - esc cancel",
		})
		return nil
	}
	return m.executeSidebarTabClose(side)
}

// sidebarTabCloseTarget resolves the tab the shortcut would close.
func (m *Model) sidebarTabCloseTarget() *activeSideConversation {
	if m == nil {
		return nil
	}
	if m.activeSide != nil && m.activeSide.ShowingSide {
		return m.activeSide
	}
	if !m.sidebarChooserVisible() || !m.sidebarChooserFocused {
		return nil
	}
	return m.sidebarTabByThreadID(m.sidebarChooserSelectedThreadID())
}

// sidebarTabByThreadID finds a live tab or the displayed side conversation.
func (m *Model) sidebarTabByThreadID(threadID string) *activeSideConversation {
	if m == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if m.activeSide != nil && strings.TrimSpace(m.activeSide.SideThreadID) == threadID {
		return m.activeSide
	}
	for _, tabs := range m.sidebarTabs {
		for _, tab := range tabs {
			if tab != nil && strings.TrimSpace(tab.SideThreadID) == threadID {
				return tab
			}
		}
	}
	return nil
}

// sidebarTabCloseLosses mirrors the Rust confirmation triggers that Go can
// observe: a running turn, unsent input, or temporary chat history.
func (m *Model) sidebarTabCloseLosses(side *activeSideConversation) []string {
	if m == nil || side == nil {
		return nil
	}
	displayed := m.activeSide == side && side.ShowingSide
	var losses []string
	if displayed && m.isTaskRunning() {
		losses = append(losses, "stop the running turn")
	}
	if displayed && !m.composerIsEmpty() {
		losses = append(losses, "discard unsent input")
	}
	if len(side.SideMessages) > 0 {
		losses = append(losses, "lose this temporary chat's history")
	}
	return losses
}

// sidebarTabCloseTitle labels the confirmation like the chooser row.
func sidebarTabCloseTitle(side *activeSideConversation) string {
	if side != nil {
		for _, message := range side.SideMessages {
			if message.Role != codextui.RoleUser {
				continue
			}
			text := firstLineText(firstNonEmpty(message.UserPrompt, message.Text))
			if text != "" {
				return codextui.TruncateToWidth(text, 24)
			}
		}
	}
	return "this side chat"
}

// respondSideTabCloseModal runs the confirmed close. A replaced dialog or a
// vanished tab is a no-op, so a stale confirmation cannot close another chat.
func (m *Model) respondSideTabCloseModal(optionID string, cancelled bool) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.pendingSideTabCloseThreadID)
	m.pendingSideTabCloseThreadID = ""
	if cancelled || optionID != sideTabCloseOption {
		m.notice = ""
		return nil
	}
	side := m.sidebarTabByThreadID(threadID)
	if side == nil {
		return nil
	}
	return m.executeSidebarTabClose(side)
}

// executeSidebarTabClose closes one tab. The displayed conversation returns to
// its parent through the existing side close path; an unfocused tab is dropped
// from the chooser and its host thread is closed in the background.
func (m *Model) executeSidebarTabClose(side *activeSideConversation) bubbletea.Cmd {
	if m == nil || side == nil {
		return nil
	}
	if m.activeSide == side && side.ShowingSide {
		return m.returnFromSideConversation()
	}
	m.unregisterSidebarTab(side)
	parentThreadID := strings.TrimSpace(side.ParentThreadID)
	if stored := m.sideConversations[parentThreadID]; stored == side {
		delete(m.sideConversations, parentThreadID)
	}
	m.abandonSideThread(side.SideThreadID)
	m.notice = "Closed side chat."
	m.refreshTranscript()
	closer := m.onCloseSide
	if closer == nil {
		return nil
	}
	params := SideCloseParams{
		ParentThreadID: side.ParentThreadID,
		SideThreadID:   side.SideThreadID,
	}
	return func() bubbletea.Msg {
		response, err := closer(params)
		return SideCloseResultMsg{Params: params, Response: response, Err: err}
	}
}
