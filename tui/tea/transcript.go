package tea

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"

	"codex_go/eventmap"
	"codex_go/protocol"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
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
	activeAssistantDeltaTurnID string
	lastFlushedAssistantItemID string
	lastFlushedAssistantTurnID string

	// replayedCloudText tracks each assistant item projected from Cloud history
	// or live streaming, keyed by turn and item, with the completion flag and
	// observation order (Rust #62403 ReplayedCloudItems).
	replayedCloudText map[string]map[string]replayedCloudItem
	// responseOrder orders completed replies and proposed plans.
	responseOrder int
	// olderResponseOrder hands negative positions to replies prepended by older
	// history pages (Rust older_response_order).
	olderResponseOrder int
	// lastCompletedResponse keeps the highest-order completed reply so a late
	// completion of an older reply cannot become the latest copyable text.
	lastCompletedResponse      string
	lastCompletedResponseOrder int
}

// replayedCloudItem is one Cloud assistant item's completion state and
// observation order within its turn.
type replayedCloudItem struct {
	finished bool
	order    int
}

// newTranscriptComponent initializes the transcript sub-component.
func newTranscriptComponent() TranscriptComponent {
	vp := viewport.New(viewport.WithWidth(defaultWidth), viewport.WithHeight(defaultHeight-defaultComposerHeight-2))
	// Rust #50209: wheel scrolling is owned by the model's configurable
	// `tui.mouse_scroll_speed` path (see tea/mouse_scroll.go); this
	// component's viewport is not fed mouse messages, so it stays on the
	// viewport default.
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
		text := msg.Text
		if msg.Role == codextui.RoleAssistant {
			text = eventmap.StripHiddenAssistantMarkup(text, false)
		}
		roleTitle := strings.ToUpper(role[:1]) + role[1:]
		lines = append(lines, roleTitle+":")
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			lines = append(lines, "  "+strings.TrimRight(line, " \t"))
		}
	}
	t.viewport.SetContent(strings.Join(lines, "\n"))
	t.viewport.SetWidth(max(width, 1))
	if t.viewport.Height() < 1 {
		t.viewport.SetHeight(1)
	}
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
	displayLines := cell.DisplayLines(t.viewport.Width())
	rawLines := cell.RawLines()
	if len(displayLines) == 0 && len(rawLines) == 0 {
		return
	}
	state.AddHistoryLines(displayLines, rawLines)
	t.updateViewportFromState(state)
}

func (t *TranscriptComponent) updateViewportFromState(state *codextui.State) {
	t.refreshTranscript(state, t.viewport.Width(), true)
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
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		state.Messages = appendAssistantDeltaToMessages(state.Messages, delta)
		state.BumpMessagesRevisionFrom(len(state.Messages) - 1)
		return
	}
	// A tool can interrupt an item and cause another stream fragment for the
	// same item. That resumed fragment is provisional too; keep it separate so
	// the authoritative completion can replace the reply at its first position
	// without mixing it with the earlier fragment (Rust #59700).
	if t.activeAssistantDeltaItemID == itemID {
		index := len(state.Messages) - 1
		if index >= 0 && state.Messages[index].Role == codextui.RoleAssistant && state.Messages[index].ItemID == itemID {
			state.Messages[index].Text += delta
			state.BumpMessagesRevisionFrom(index)
			return
		}
	}
	state.Messages = append(state.Messages, codextui.Message{Role: codextui.RoleAssistant, Text: delta, ItemID: itemID})
	state.BumpMessagesRevisionFrom(len(state.Messages) - 1)
	t.activeAssistantDeltaItemID = itemID
}

// mergeAssistantFinal merges the final assistant message text.
func (t *TranscriptComponent) mergeAssistantFinal(state *codextui.State, text string, width int) {
	if t == nil || state == nil {
		return
	}
	state.Messages = mergeAssistantFinalToMessages(state.Messages, text)
	state.BumpMessagesRevisionFrom(len(state.Messages) - 1)
}

// mergeAssistantFinalForItem repairs the reply that was already committed at a
// tool boundary. The completed item is authoritative, while intervening tool
// cells keep their order and later fragments of the same item are discarded
// (Rust #59700 CorrectFlushedAgentMessage).
func (t *TranscriptComponent) mergeAssistantFinalForItem(state *codextui.State, itemID string, text string, width int) {
	if t == nil || state == nil {
		return
	}
	if t.replaceAssistantMessageForItem(state, itemID, text, "") {
		t.noteCompletedReply(itemID, text)
		return
	}
	// Rust #62403: a late completion of another reply must not be merged into
	// the reply that is still streaming. Keep them separate so each keeps its
	// own copy source and the live stream is not interrupted.
	if itemID = strings.TrimSpace(itemID); itemID != "" && t.activeAssistantDeltaItemID != "" &&
		t.activeAssistantDeltaItemID != itemID && strings.TrimSpace(text) != "" {
		completed := codextui.NormalizeCompletedAssistantMarkdown(text)
		if completed != "" {
			state.Messages = append(state.Messages, codextui.Message{
				Role:    codextui.RoleAssistant,
				Text:    completed,
				RawText: completed,
				ItemID:  itemID,
			})
			state.BumpMessagesRevisionFrom(len(state.Messages) - 1)
		}
		t.noteCompletedReply(itemID, completed)
		return
	}
	state.Messages = mergeAssistantFinalToMessages(state.Messages, text)
	state.BumpMessagesRevisionFrom(len(state.Messages) - 1)
	t.noteCompletedReply(itemID, text)
}

// noteReplayedCloudItem records one Cloud assistant item. Older history pages
// receive negative positions so they stay older than anything observed live.
func (t *TranscriptComponent) noteReplayedCloudItem(turnID string, itemID string, finished bool, older bool) bool {
	if t == nil {
		return false
	}
	turnID = strings.TrimSpace(turnID)
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return false
	}
	if t.replayedCloudText == nil {
		t.replayedCloudText = map[string]map[string]replayedCloudItem{}
	}
	items := t.replayedCloudText[turnID]
	if items == nil {
		items = map[string]replayedCloudItem{}
		t.replayedCloudText[turnID] = items
	}
	entry, seen := items[itemID]
	if !seen {
		if older {
			t.olderResponseOrder--
			entry.order = t.olderResponseOrder
		} else {
			t.responseOrder++
			entry.order = t.responseOrder
		}
	}
	entry.finished = entry.finished || finished
	items[itemID] = entry
	return !seen
}

// NoteReplayedAssistantMessages records assistant items projected from thread
// history so a late live completion keeps the replay position for copy ordering
// (Rust #62403). A message with visible text counts as finished.
func (t *TranscriptComponent) NoteReplayedAssistantMessages(messages []codextui.Message) {
	if t == nil {
		return
	}
	for _, message := range messages {
		if message.Role != codextui.RoleAssistant {
			continue
		}
		itemID := strings.TrimSpace(message.ItemID)
		if itemID == "" {
			continue
		}
		t.noteReplayedCloudItem("", itemID, strings.TrimSpace(message.Text) != "", false)
	}
}

// NoteOlderAssistantMessages records replies spliced in from an older history
// page. They take negative positions, so they always sort before anything the
// view observed live (Rust #62403 older_response_order).
func (t *TranscriptComponent) NoteOlderAssistantMessages(messages []codextui.Message) {
	if t == nil {
		return
	}
	for _, message := range messages {
		if message.Role != codextui.RoleAssistant {
			continue
		}
		itemID := strings.TrimSpace(message.ItemID)
		if itemID == "" {
			continue
		}
		t.noteReplayedCloudItem("", itemID, strings.TrimSpace(message.Text) != "", true)
	}
}

// ReplayedCloudItemFinished mirrors Rust has_replayed_cloud_text: only an item
// recorded as finished may suppress its live completion.
func (t *TranscriptComponent) ReplayedCloudItemFinished(turnID string, itemID string) bool {
	if t == nil {
		return false
	}
	items := t.replayedCloudText[strings.TrimSpace(turnID)]
	entry, ok := items[strings.TrimSpace(itemID)]
	return ok && entry.finished
}

// noteCompletedReply orders a completed reply and keeps the highest-order text
// as the latest copyable response.
func (t *TranscriptComponent) noteCompletedReply(itemID string, text string) {
	if t == nil {
		return
	}
	order := 0
	if itemID = strings.TrimSpace(itemID); itemID != "" {
		if t.replayedCloudText == nil {
			t.replayedCloudText = map[string]map[string]replayedCloudItem{}
		}
		items := t.replayedCloudText[""]
		if items == nil {
			items = map[string]replayedCloudItem{}
			t.replayedCloudText[""] = items
		}
		entry := items[itemID]
		if entry.order == 0 {
			// A first observation takes the next position; a reply that was
			// already replayed keeps its recorded position so its late
			// completion stays older for copy ordering (Rust #62403).
			t.responseOrder++
			entry.order = t.responseOrder
		}
		entry.finished = true
		order = entry.order
		items[itemID] = entry
	} else {
		t.responseOrder++
		order = t.responseOrder
	}
	if text = strings.TrimSpace(text); text != "" && order > t.lastCompletedResponseOrder {
		t.lastCompletedResponse = text
		t.lastCompletedResponseOrder = order
	}
}

// LatestCompletedResponse returns the highest-order completed reply text
// (Rust #62403's copy-order bookkeeping), so a late completion of an older
// reply never replaces a newer copy source.
func (t *TranscriptComponent) LatestCompletedResponse() string {
	if t == nil {
		return ""
	}
	return t.lastCompletedResponse
}

func (t *TranscriptComponent) replaceAssistantMessageForItem(state *codextui.State, itemID string, text string, phase string) bool {
	if t == nil || state == nil {
		return false
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return false
	}
	text = codextui.NormalizeCompletedAssistantMarkdown(text)
	if text == "" {
		// Rust #62403: a completion that contains only hidden directives owns
		// the reply; drop the restored or streamed prefix instead of leaving it
		// in the transcript.
		kept := state.Messages[:0]
		removed := false
		for _, message := range state.Messages {
			if message.Role == codextui.RoleAssistant && strings.TrimSpace(message.ItemID) == itemID {
				removed = true
				continue
			}
			kept = append(kept, message)
		}
		if !removed {
			return false
		}
		state.Messages = kept
		if t.lastFlushedAssistantItemID == itemID {
			t.lastFlushedAssistantItemID = ""
			t.lastFlushedAssistantTurnID = ""
		}
		state.BumpMessagesRevision()
		return true
	}
	first := -1
	for index, message := range state.Messages {
		if message.Role == codextui.RoleAssistant && strings.TrimSpace(message.ItemID) == itemID {
			first = index
			break
		}
	}
	if first < 0 {
		return false
	}
	kept := make([]codextui.Message, 0, len(state.Messages))
	for index, message := range state.Messages {
		if index != first && message.Role == codextui.RoleAssistant && strings.TrimSpace(message.ItemID) == itemID {
			continue
		}
		kept = append(kept, message)
	}
	for index := range kept {
		if kept[index].Role != codextui.RoleAssistant || strings.TrimSpace(kept[index].ItemID) != itemID {
			continue
		}
		kept[index].Text = text
		kept[index].RawText = text
		kept[index].InterruptedItem = ""
		if phase != "" {
			kept[index].Phase = phase
		}
		state.Messages = kept
		state.BumpMessagesRevisionFrom(index)
		if t.lastFlushedAssistantItemID == itemID {
			t.lastFlushedAssistantItemID = ""
			t.lastFlushedAssistantTurnID = ""
		}
		return true
	}
	return false
}

// completeAssistantCommentary fixes the streamed preamble in transcript history
// before a tool cell is inserted. Rust treats commentary as a completed
// mid-turn message rather than leaving it as the active final-answer stream.
func (t *TranscriptComponent) completeAssistantCommentary(state *codextui.State, itemID string, text string, width int) {
	if t == nil || state == nil || strings.TrimSpace(text) == "" {
		return
	}
	if t.replaceAssistantMessageForItem(state, itemID, text, messagePhaseCommentary) {
		t.activeAssistantDeltaItemID = ""
		return
	}
	state.Messages = mergeAssistantPhaseToMessages(state.Messages, text, messagePhaseCommentary)
	state.BumpMessagesRevisionFrom(len(state.Messages) - 1)
	t.activeAssistantDeltaItemID = ""
}

// finishAssistantPreambleBeforeTool closes the active streamed assistant item
// at the tool boundary. The text is already present in State.Messages; only
// the streaming identity must be reset so later final-answer deltas cannot
// merge back into or replace the preamble.
func (t *TranscriptComponent) finishAssistantPreambleBeforeTool(state *codextui.State) {
	if t == nil {
		return
	}
	if state != nil && t.activeAssistantDeltaItemID != "" {
		key := interruptedAgentMessageKey(t.activeAssistantDeltaTurnID, t.activeAssistantDeltaItemID)
		for index := len(state.Messages) - 1; index >= 0; index-- {
			message := &state.Messages[index]
			if message.Role != codextui.RoleAssistant || strings.TrimSpace(message.ItemID) != t.activeAssistantDeltaItemID {
				continue
			}
			message.InterruptedItem = key
			state.BumpMessagesRevisionFrom(index)
			t.lastFlushedAssistantItemID = t.activeAssistantDeltaItemID
			t.lastFlushedAssistantTurnID = t.activeAssistantDeltaTurnID
			break
		}
	}
	t.activeAssistantDeltaItemID = ""
	t.activeAssistantDeltaTurnID = ""
}

func interruptedAgentMessageKey(turnID string, itemID string) string {
	turnID = strings.TrimSpace(turnID)
	itemID = strings.TrimSpace(itemID)
	if turnID == "" || itemID == "" {
		return ""
	}
	return turnID + ":" + itemID
}

// insertFinalMessageSeparatorIfNeeded inserts a separator between tool calls and assistant message.
func (t *TranscriptComponent) insertFinalMessageSeparatorIfNeeded(state *codextui.State, width int, turnID string) {
	if t == nil || state == nil || !t.needsFinalMessageSeparator {
		return
	}
	if index := len(state.Messages) - 1; index >= 0 && state.Messages[index].Role == codextui.RoleAssistant {
		return
	}
	if width < 20 {
		width = 20
	}
	// Rust #43558: a live completion falls back to the local clock when no saved
	// timestamp is available.
	cell := historycell.NewFinalMessageSeparator(nil, nil).WithCompletedAt(time.Now())
	state.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
	// The separator reports this turn's boundary; pagination anchors a split
	// turn's generated image on it (Rust FinalMessageSeparator::turn_id).
	if index := len(state.Messages) - 1; index >= 0 && strings.TrimSpace(turnID) != "" {
		state.Messages[index].TurnID = strings.TrimSpace(turnID)
	}
	t.needsFinalMessageSeparator = false
}

// appendCompletionFooter adds the turn's completion metadata after its final
// answer (Rust #43558). The footer is emitted once per completed turn; a live
// completion falls back to the local clock when no saved timestamp exists.
// elapsedSeconds comes from the protocol turn duration and is only shown when
// it exceeds sixty seconds.
func (t *TranscriptComponent) appendCompletionFooter(state *codextui.State, width int, elapsedSeconds *int64, completedAt time.Time, turnID string) {
	if t == nil || state == nil {
		return
	}
	if width < 20 {
		width = 20
	}
	cell := historycell.NewFinalMessageSeparator(elapsedSeconds, nil).WithCompletedAt(completedAt)
	state.AddHistoryLines(cell.DisplayLines(width), cell.RawLines())
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
// The stored body keeps the response's own trailing whitespace (Markdown hard
// breaks and code padding, Rust #48549); only trailing blank lines and a
// trailing newline are dropped.
func mergeAssistantFinalToMessages(messages []codextui.Message, text string) []codextui.Message {
	return mergeAssistantPhaseToMessages(messages, text, "")
}

// mergeAssistantPhaseToMessages merges assistant text and records the known
// completed-message phase so later consumers (such as /rename suggestions) can
// omit commentary.
func mergeAssistantPhaseToMessages(messages []codextui.Message, text string, phase string) []codextui.Message {
	text = codextui.NormalizeCompletedAssistantMarkdown(text)
	if text == "" {
		return messages
	}
	index := len(messages) - 1
	if index >= 0 && messages[index].Role == codextui.RoleAssistant {
		current := codextui.NormalizeCompletedAssistantMarkdown(messages[index].Text)
		switch {
		case current == text:
			if phase != "" {
				messages[index].Phase = phase
			}
			return messages
		case strings.Contains(text, current):
			messages[index].Text = text
			if phase != "" {
				messages[index].Phase = phase
			}
			return messages
		}
	}
	if assistantFinalExistsInCurrentTurn(messages, text) {
		return messages
	}
	return append(messages, codextui.Message{Role: codextui.RoleAssistant, Text: text, Phase: phase})
}

// applyBufferedThreadEventToMessages replays one buffered background-thread
// event onto a message snapshot. It mirrors the subset of live transcript
// rendering that matters when switching to a running agent (Rust parity).
// bufferedReplayPrivacy carries the per-turn voice-handoff state across one
// thread's buffered replay. Rust ThreadEventStore::snapshot applies the same
// filter before the rebuilt widget replays the thread.
type bufferedReplayPrivacy struct {
	delegated map[string]bool
	typed     map[string]bool
	// currentTurn is the turn opened by the last turn.started event. The
	// app/remote_tui.go bridge builds item notifications with
	// protocol.AgentMessageDelta / protocol.ItemCompleted, which carry no
	// TurnID, so the replay reducer has to attribute those items to the turn
	// opened by the preceding turn.started. Without it a delegated voice turn
	// replays with turnID "", the marker is never recorded and its private
	// reasoning/commentary leaks back into the transcript.
	currentTurn string
}

// turnID reports the turn an event belongs to: the event's own identity when
// the producer supplied one, otherwise the turn opened by the last
// turn.started (Rust replays every buffered event inside its turn, and
// tui/tea/side_voice_privacy.go resolves side-conversation events the same
// way).
func (s *bufferedReplayPrivacy) turnID(event protocol.ThreadEvent) string {
	if id := strings.TrimSpace(firstNonEmpty(event.TurnID, event.InternalTurnID)); id != "" {
		return id
	}
	if s == nil {
		return ""
	}
	return s.currentTurn
}

// applyBufferedThreadReplay replays a background thread's buffered events with
// the voice-handoff privacy filter applied: the spoken prompt is shown without
// its <realtime_delegation> envelope, and a delegated turn keeps only its
// public output (Rust hide_private_items_after_voice_handoff).
func applyBufferedThreadReplay(messages []codextui.Message, events []protocol.ThreadEvent) []codextui.Message {
	state := bufferedReplayPrivacy{delegated: map[string]bool{}, typed: map[string]bool{}}
	for _, event := range events {
		messages = state.apply(messages, event)
	}
	return messages
}

func (s *bufferedReplayPrivacy) apply(messages []codextui.Message, event protocol.ThreadEvent) []codextui.Message {
	if event.Type == "turn.started" {
		if id := strings.TrimSpace(firstNonEmpty(event.TurnID, event.InternalTurnID)); id != "" {
			s.currentTurn = id
		}
	}
	turnID := s.turnID(event)
	if event.Item != nil && (event.Type == "item.started" || event.Type == "item.completed") {
		if realtimeItemIsDelegationUserMessage(event.Item) {
			if turnID != "" {
				s.delegated[turnID] = true
			}
			if event.Type != "item.completed" {
				return messages
			}
			// Rust on_committed_user_message(from_replay=true) renders the spoken
			// input instead of the delegation envelope.
			projected := *event.Item
			if inner, ok := chatwidget.RealtimeDelegationInput(firstNonEmpty(projected.Text, projected.Message)); ok {
				projected.Text = chatwidget.RealtimeDelegationDisplayText(inner)
				projected.Message = ""
				if projected.Content != nil {
					projected.Content = nil
				}
			}
			return applyBufferedThreadItemToMessages(messages, &projected)
		}
		switch chatwidget.NormalizeItemKind(event.Item.Type) {
		case "reasoning", "agentmessage", "assistantmessage":
			if !s.delegated[turnID] {
				s.typed[realtimeItemKey(turnID, event.Item.ID)] = true
			}
		}
	}
	if s.suppressed(event) {
		return messages
	}
	return applyBufferedThreadEventToMessages(messages, event)
}

func (s *bufferedReplayPrivacy) suppressed(event protocol.ThreadEvent) bool {
	turnID := s.turnID(event)
	if !s.delegated[turnID] {
		return false
	}
	switch event.Type {
	case "item.delta":
		if event.Delta == nil || strings.TrimSpace(event.Delta.Text) == "" {
			return false
		}
		return !s.typed[realtimeItemKey(turnID, event.Delta.ItemID)]
	case "item.reasoning.delta":
		return event.Delta == nil || !s.typed[realtimeItemKey(turnID, event.Delta.ItemID)]
	case "item.completed":
		if event.Item == nil {
			return false
		}
		if !chatwidget.IsPrivateRealtimeAgentItem(event.Item.Type, event.Item.Phase, firstNonEmpty(event.Item.Text, event.Item.Message)) {
			return false
		}
		return !s.typed[realtimeItemKey(turnID, event.Item.ID)]
	}
	return false
}

func applyBufferedThreadEventToMessages(messages []codextui.Message, event protocol.ThreadEvent) []codextui.Message {
	switch event.Type {
	case "item.delta":
		if event.Delta != nil && strings.TrimSpace(event.Delta.Text) != "" {
			return appendAssistantDeltaToMessages(messages, event.Delta.Text)
		}
	case "item.completed":
		if event.Item != nil {
			return applyBufferedThreadItemToMessages(messages, event.Item)
		}
	case "turn.failed", "error":
		text := "Unknown error"
		if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
			text = strings.TrimSpace(event.Error.Message)
		}
		return append(messages, codextui.Message{Role: codextui.RoleSystem, Text: "Error: " + text})
	}
	return messages
}

// filterResolvedMisalignmentReplayEvents mirrors Rust #42399: an earlier turn's
// misalignment violation is resolved once a later turn exists. Keep the latest
// turn's error so a currently stopped chat is still reported.
func filterResolvedMisalignmentReplayEvents(events []protocol.ThreadEvent) []protocol.ThreadEvent {
	latestTurnID := ""
	for index := len(events) - 1; index >= 0; index-- {
		if turnID := strings.TrimSpace(events[index].TurnID); turnID != "" {
			latestTurnID = turnID
			break
		}
	}
	if latestTurnID == "" {
		return events
	}
	filtered := make([]protocol.ThreadEvent, 0, len(events))
	for _, event := range events {
		if (event.Type == "turn.failed" || event.Type == "error") &&
			strings.TrimSpace(event.TurnID) != "" &&
			strings.TrimSpace(event.TurnID) != latestTurnID &&
			event.Error != nil &&
			chatwidget.IsMisalignmentPolicyViolationMessage(event.Error.Message) {
			continue
		}
		filtered = append(filtered, event)
	}
	return filtered
}

func applyBufferedThreadItemToMessages(messages []codextui.Message, item *protocol.ThreadItem) []codextui.Message {
	if item == nil {
		return messages
	}
	switch item.Type {
	case "user_message", "userMessage":
		text := strings.TrimSpace(firstNonEmpty(item.Text, item.Message))
		if text == "" {
			return messages
		}
		// Rust #46486: a desktop async-question reply renders as readable
		// question-and-answer text instead of the raw reply envelope.
		if replies := codextui.ParseAsyncQuestionReplies(text); len(replies) > 0 {
			display, _ := codextui.AsyncQuestionReplyDisplayText(text)
			return append(messages, codextui.Message{
				Role:                 codextui.RoleUser,
				Text:                 display,
				RawText:              display,
				UserPrompt:           display,
				AsyncQuestionReplies: replies,
			})
		}
		return append(messages, codextui.Message{Role: codextui.RoleUser, Text: text, RawText: text})
	case "agent_message":
		if questions := bottompane.ParseAsyncUserInputQuestions(item.Metadata["questions"]); len(questions) > 0 {
			message := codextui.Message{
				Role:           codextui.RoleAssistant,
				Text:           strings.TrimSpace(item.Text),
				RawText:        strings.TrimSpace(item.Text),
				AsyncQuestions: messageAsyncQuestions(questions),
			}
			if strings.TrimSpace(message.Text) != "" {
				return append(messages, message)
			}
			return messages
		}
		return mergeAssistantFinalToMessages(messages, item.Text)
	case "plan":
		return mergeAssistantFinalToMessages(messages, item.Text)
	case "command_execution":
		command := strings.TrimSpace(item.Command)
		if command == "" {
			command = strings.TrimSpace(item.Text)
		}
		if command == "" {
			return messages
		}
		text := "$ " + command
		return append(messages, codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text})
	case "file_change":
		text := fmt.Sprintf("Updated %d file(s)", len(item.Changes))
		return append(messages, codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text})
	case "collab_tool_call", "collabAgentToolCall", "collab_agent_tool_call":
		if text := bufferedCollabToolCallSummary(item); text != "" {
			return append(messages, codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text})
		}
	case "sub_agent_activity", "subAgentActivity":
		action := ""
		switch strings.ToLower(strings.TrimSpace(item.ActivityKind)) {
		case "started":
			action = "Started"
		case "interacted":
			action = "Contacted"
		case "interrupted":
			action = "Interrupted"
		case "completed":
			action = "Completed"
		}
		if action == "" {
			return messages
		}
		path := strings.TrimSpace(item.AgentPath)
		if path == "" {
			path = strings.TrimSpace(item.AgentThreadID)
		}
		text := action
		if path != "" {
			text += " " + path
		}
		return append(messages, codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text})
	case "contextCompaction", "context_compaction":
		return append(messages, codextui.Message{Role: codextui.RoleHistory, Text: "Context compacted", RawText: "Context compacted"})
	}
	return messages
}

func bufferedCollabToolCallSummary(item *protocol.ThreadItem) string {
	if item == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(item.ToolName)) {
	case "spawn_agent":
		return "Spawned an agent"
	case "send_input", "send_message":
		return "Sent input to an agent"
	case "resume_agent":
		return "Resumed an agent"
	case "wait", "wait_agent":
		return "Waited for an agent"
	case "close_agent", "interrupt_agent":
		return "Closed an agent"
	}
	text := strings.TrimSpace(item.Text)
	if text == "" {
		text = strings.TrimSpace(item.Message)
	}
	return text
}

// assistantFinalExistsInCurrentTurn checks if the final text already exists in current turn.
func assistantFinalExistsInCurrentTurn(messages []codextui.Message, text string) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		switch message.Role {
		case codextui.RoleUser:
			return false
		case codextui.RoleAssistant:
			// Compare the same normalized body the caller passes in, so a
			// response that keeps its trailing hard break is still recognized
			// as already present in this turn.
			if codextui.NormalizeCompletedAssistantMarkdown(message.Text) == text {
				return true
			}
		}
	}
	return false
}

// normalizedErrorHistoryMessage mirrors Rust's new_error_event: the cell keeps
// the message verbatim behind a single bullet marker, so no "Error: " prefix is
// added (Rust history_cell::notices::new_error_event).
func normalizedErrorHistoryMessage(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "Unknown error"
	}
	return message
}

// taskStartedAt is a helper for computing elapsed time.
var defaultNow = time.Now
