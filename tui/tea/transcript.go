package tea

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
)

// TranscriptComponent manages the chat message display area: the viewport,
// transcript overlay, tool call tracking, and message/cell manipulation.
// It is embedded in Model for method promotion during the transition period.
type TranscriptComponent struct {
	viewport          viewport.Model
	activityFollow    bool
	overlay           *chatwidget.TranscriptOverlay
	overlayAltScreen  bool
	overlayTranscript bool

	// Tool call display state
	toolCalls          map[string]*toolCallDisplayState
	mcpToolCalls       map[string]*mcpToolCallDisplayState
	startedThreadIDs   map[string]bool
	completedThreadIDs map[string]bool

	// Message tracking
	lastTurnError              string
	needsFinalMessageSeparator bool
	activeAssistantDeltaItemID string
}

// newTranscriptComponent initializes the transcript sub-component.
func newTranscriptComponent() TranscriptComponent {
	vp := viewport.New(defaultWidth, defaultHeight-defaultComposerHeight-2)
	vp.MouseWheelEnabled = true
	vp.MouseWheelDelta = 3
	return TranscriptComponent{
		viewport:           vp,
		activityFollow:     true,
		toolCalls:          make(map[string]*toolCallDisplayState),
		mcpToolCalls:       make(map[string]*mcpToolCallDisplayState),
		startedThreadIDs:   make(map[string]bool),
		completedThreadIDs: make(map[string]bool),
	}
}

// refreshTranscript updates the viewport content from State messages.
// Called from Model.View() before rendering.
func (t *TranscriptComponent) refreshTranscript(state *codextui.State, width int, showChrome bool) {
	if state == nil {
		return
	}
	messages := state.Messages
	lines := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == codextui.RoleHistory {
			lines = append(lines, strings.TrimRight(msg.Text, "\r\n"))
			continue
		}
		role := string(msg.Role)
		if role == "" {
			role = string(codextui.RoleSystem)
		}
		roleTitle := strings.ToUpper(role[:1]) + role[1:]
		lines = append(lines, roleTitle+":")
		for _, line := range strings.Split(strings.TrimSpace(msg.Text), "\n") {
			lines = append(lines, "  "+strings.TrimRight(line, " \t"))
		}
	}
	t.viewport.SetContent(strings.Join(lines, "\n"))
	t.viewport.Width = max(width, 1)
	t.viewport.Height = max(t.viewport.Height, 1)
	if t.activityFollow {
		t.viewport.GotoBottom()
	}
}

// markThreadStarted records a thread ID as having an active turn in progress.
func (t *TranscriptComponent) markThreadStarted(threadID string) {
	if t == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	t.startedThreadIDs[threadID] = true
}

// markThreadCompleted records a thread ID as finished.
func (t *TranscriptComponent) markThreadCompleted(threadID string) {
	if t == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	t.completedThreadIDs[threadID] = true
}

// clearCurrentThreadAfterFailure cleans up thread tracking after a fatal error.
func (t *TranscriptComponent) clearCurrentThreadAfterFailure(state *codextui.State, message string) {
	if t == nil || state == nil {
		return
	}
	threadID := strings.TrimSpace(state.ThreadID)
	if threadID == "" {
		return
	}
	lower := strings.ToLower(strings.TrimSpace(message))
	if strings.Contains(lower, "thread not found") || (t.startedThreadIDs[threadID] && !t.completedThreadIDs[threadID]) {
		state.SetThreadID("")
		delete(t.startedThreadIDs, threadID)
		delete(t.completedThreadIDs, threadID)
	}
}

// addHistoryCell appends a history cell and updates the State messages.
func (t *TranscriptComponent) addHistoryCell(state *codextui.State, cell historycell.HistoryCell, hasMCPStartupOverlay bool) {
	if state == nil || t == nil {
		return
	}
	displayLines := cell.DisplayLines(t.viewport.Width)
	rawLines := cell.RawLines()
	if len(displayLines) == 0 && len(rawLines) == 0 {
		return
	}
	state.AddHistoryLines(displayLines, rawLines)
	t.updateViewportFromState(state)
}

func (t *TranscriptComponent) updateViewportFromState(state *codextui.State) {
	t.refreshTranscript(state, t.viewport.Width, true)
}

// Viewport returns the underlying viewport model for Bubble Tea Update delegation.
func (t *TranscriptComponent) Viewport() *viewport.Model {
	if t == nil {
		return nil
	}
	return &t.viewport
}

// Overlay returns the active transcript overlay, if any.
func (t *TranscriptComponent) Overlay() *chatwidget.TranscriptOverlay {
	if t == nil {
		return nil
	}
	return t.overlay
}

// SetOverlay sets the transcript overlay.
func (t *TranscriptComponent) SetOverlay(o *chatwidget.TranscriptOverlay) {
	if t != nil {
		t.overlay = o
	}
}

// IsActivityFollow returns whether the viewport auto-scrolls.
func (t *TranscriptComponent) IsActivityFollow() bool {
	if t == nil {
		return false
	}
	return t.activityFollow
}

// ToolCallsStarted returns the count of active tool calls.
func (t *TranscriptComponent) ToolCallsStarted() int {
	if t == nil {
		return 0
	}
	return len(t.toolCalls)
}

// addErrorHistoryMessage adds an error message to the history.
func (t *TranscriptComponent) addErrorHistoryMessage(state *codextui.State, message string, width int) {
	if t == nil || state == nil {
		return
	}
	message = normalizedErrorHistoryMessage(message)
	t.addHistoryCellWithWidth(state, historycell.NewErrorEvent(message), width)
}

// addTurnErrorHistoryMessage adds a turn error message, deduplicating against lastTurnError.
func (t *TranscriptComponent) addTurnErrorHistoryMessage(state *codextui.State, message string, width int) {
	if t == nil || state == nil {
		return
	}
	message = normalizedErrorHistoryMessage(message)
	if message == t.lastTurnError {
		return
	}
	t.lastTurnError = message
	t.addHistoryCellWithWidth(state, historycell.NewErrorEvent(message), width)
}

// addInfoHistoryMessage adds an info message to the history.
func (t *TranscriptComponent) addInfoHistoryMessage(state *codextui.State, message string, width int) {
	if t == nil || state == nil {
		return
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	t.addHistoryCellWithWidth(state, historycell.NewInfoEvent(message, ""), width)
}

// addHistoryCellWithWidth adds a history cell with explicit width (used by migrated methods).
func (t *TranscriptComponent) addHistoryCellWithWidth(state *codextui.State, cell historycell.HistoryCell, width int) {
	if t == nil || state == nil {
		return
	}
	if width < 20 {
		width = 20
	}
	state.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
}

// appendAssistantDelta appends streaming delta text to the assistant message.
func (t *TranscriptComponent) appendAssistantDelta(state *codextui.State, itemID string, delta string, width int) {
	if t == nil || state == nil || delta == "" {
		return
	}
	t.insertFinalMessageSeparatorIfNeeded(state, width)
	itemID = strings.TrimSpace(itemID)
	if itemID != "" && t.activeAssistantDeltaItemID != "" && itemID != t.activeAssistantDeltaItemID {
		state.Messages = append(state.Messages, codextui.Message{Role: codextui.RoleAssistant, Text: delta})
		t.activeAssistantDeltaItemID = itemID
		return
	}
	state.Messages = appendAssistantDeltaToMessages(state.Messages, delta)
	if itemID != "" {
		t.activeAssistantDeltaItemID = itemID
	}
}

// mergeAssistantFinal merges the final assistant message text.
func (t *TranscriptComponent) mergeAssistantFinal(state *codextui.State, text string, width int) {
	if t == nil || state == nil {
		return
	}
	if strings.TrimSpace(text) != "" {
		t.insertFinalMessageSeparatorIfNeeded(state, width)
	}
	state.Messages = mergeAssistantFinalToMessages(state.Messages, text)
}

// insertFinalMessageSeparatorIfNeeded inserts a separator between tool calls and assistant message.
func (t *TranscriptComponent) insertFinalMessageSeparatorIfNeeded(state *codextui.State, width int) {
	if t == nil || state == nil || !t.needsFinalMessageSeparator {
		return
	}
	if index := len(state.Messages) - 1; index >= 0 && state.Messages[index].Role == codextui.RoleAssistant {
		return
	}
	if width < 20 {
		width = 20
	}
	cell := historycell.NewFinalMessageSeparator(nil, nil)
	state.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
	t.needsFinalMessageSeparator = false
}

// appendAssistantDeltaToMessages is a helper for appending delta text.
func appendAssistantDeltaToMessages(messages []codextui.Message, delta string) []codextui.Message {
	if delta == "" {
		return messages
	}
	index := len(messages) - 1
	if index >= 0 && messages[index].Role == codextui.RoleAssistant {
		messages[index].Text += delta
		return messages
	}
	return append(messages, codextui.Message{Role: codextui.RoleAssistant, Text: delta})
}

// mergeAssistantFinalToMessages is a helper for merging final assistant text.
func mergeAssistantFinalToMessages(messages []codextui.Message, text string) []codextui.Message {
	text = strings.TrimSpace(text)
	if text == "" {
		return messages
	}
	index := len(messages) - 1
	if index >= 0 && messages[index].Role == codextui.RoleAssistant {
		current := strings.TrimSpace(messages[index].Text)
		switch {
		case current == text:
			return messages
		case strings.Contains(text, current):
			messages[index].Text = text
			return messages
		}
	}
	if assistantFinalExistsInCurrentTurn(messages, text) {
		return messages
	}
	return append(messages, codextui.Message{Role: codextui.RoleAssistant, Text: text})
}

// assistantFinalExistsInCurrentTurn checks if the final text already exists in current turn.
func assistantFinalExistsInCurrentTurn(messages []codextui.Message, text string) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		switch message.Role {
		case codextui.RoleUser:
			return false
		case codextui.RoleAssistant:
			if strings.TrimSpace(message.Text) == text {
				return true
			}
		}
	}
	return false
}

// normalizedErrorHistoryMessage ensures error messages have "Error: " prefix.
func normalizedErrorHistoryMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Unknown error"
	}
	if !strings.HasPrefix(strings.ToLower(message), "error:") {
		message = "Error: " + message
	}
	return message
}

// taskStartedAt is a helper for computing elapsed time.
var defaultNow = time.Now
