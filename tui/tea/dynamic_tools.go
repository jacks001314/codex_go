package tea

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/protocol"
	agentsoverview "codex_go/tui/agents_overview"
	"codex_go/tui/history_cell"
)

// taskMentionSearchDebounce mirrors Rust task_mentions::SEARCH_DEBOUNCE.
const taskMentionSearchDebounce = 100 * time.Millisecond

// nextTaskSearchGeneration bumps the task-search generation and returns it
// together with the shared counter the debounced search command checks, so a
// superseded request is dropped before it reaches the app server (Rust's
// AtomicU64 generation in task_mentions::spawn_search).
func (m *Model) nextTaskSearchGeneration() (uint64, *atomic.Uint64) {
	m.mentionTaskSearchGeneration++
	if m.taskSearchGeneration == nil {
		m.taskSearchGeneration = new(atomic.Uint64)
	}
	m.taskSearchGeneration.Store(m.mentionTaskSearchGeneration)
	return m.mentionTaskSearchGeneration, m.taskSearchGeneration
}

// DynamicToolThreadStartedMsg reports a task the TUI started or resumed while
// serving a codex_tui dynamic tool call (Rust
// AppEvent::DynamicToolThreadStarted). The dashboard tracks it as a dispatched
// task.
type DynamicToolThreadStartedMsg struct {
	ThreadID string
	// Thread carries the lifecycle response's metadata when available, so the
	// dashboard can register a task before its next list refresh (Rust #46946).
	Thread *appserver.Thread
	// TaskToolsAvailable records whether the child also hosts the task-tool
	// namespace (Rust app_server.remember_task_tool_thread).
	TaskToolsAvailable bool
}

// TaskToolsAvailableMsg reports whether a thread's app server accepted the
// codex_tui task-tool namespace. Task mentions are only enabled for threads that
// host it (Rust AppServerSession::task_tools_available /
// chat_widget.set_task_mentions_enabled).
type TaskToolsAvailableMsg struct {
	ThreadID  string
	Available bool
}

// applyTaskToolsAvailable records the capability, which gates the mention
// popup's task search.
func (m *Model) applyTaskToolsAvailable(message TaskToolsAvailableMsg) {
	if m == nil {
		return
	}
	threadID := strings.TrimSpace(message.ThreadID)
	if threadID == "" {
		return
	}
	// Rust only remembers positive capability (AppServerSession::task_tools_available
	// is a set plus the persisted marker directory); a thread whose start was
	// downgraded reports no capability instead of forgetting an earlier one.
	if !message.Available {
		return
	}
	if m.taskToolThreads == nil {
		m.taskToolThreads = map[string]bool{}
	}
	m.taskToolThreads[threadID] = true
}

// taskMentionsEnabled reports whether the current thread can reference tasks
// (Rust chat_widget.task_mentions_enabled, set from task_tools_available).
func (m *Model) taskMentionsEnabled() bool {
	if m == nil || m.onSearchTasks == nil {
		return false
	}
	threadID := m.currentThreadID()
	if threadID == "" {
		return false
	}
	if available, ok := m.taskToolThreads[threadID]; ok {
		return available
	}
	// A thread started by an earlier process keeps its persisted capability
	// (Rust AppServerSession::task_tools_available marker directory).
	if m.onTaskToolsAvailable != nil {
		return m.onTaskToolsAvailable(threadID)
	}
	return false
}

// inheritTaskToolCapability mirrors Rust's fork handling: forking an available
// thread keeps the namespace for the child (the fork carries the parent's
// capability) instead of re-registering it.
func (m *Model) inheritTaskToolCapability(parentThreadID string, childThreadID string) {
	if m == nil {
		return
	}
	parentThreadID = strings.TrimSpace(parentThreadID)
	childThreadID = strings.TrimSpace(childThreadID)
	if parentThreadID == "" || childThreadID == "" || !m.taskToolThreads[parentThreadID] {
		return
	}
	if m.taskToolThreads == nil {
		m.taskToolThreads = map[string]bool{}
	}
	m.taskToolThreads[childThreadID] = true
}

// applyDynamicToolThreadStarted records the dispatched task and refreshes an open
// dashboard so it appears immediately.
func (m *Model) applyDynamicToolThreadStarted(msg DynamicToolThreadStartedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	threadID := strings.TrimSpace(msg.ThreadID)
	if threadID == "" {
		return nil
	}
	if m.dynamicToolThreads == nil {
		m.dynamicToolThreads = map[string]bool{}
	}
	m.dynamicToolThreads[threadID] = true
	if m.agentsOverview == nil {
		return nil
	}
	if msg.Thread != nil && !msg.Thread.Ephemeral {
		m.registerAgentsOverviewThreadMetadata(msg.Thread)
	}
	// Refresh the open dashboard so the dispatched task appears; Rust also marks
	// it as dispatched before the next list refresh.
	return m.refreshAgentsOverviewCmd()
}

// applyDynamicToolItem retains one dynamic tool row and updates it in place by
// call id, so concurrent completions keep their original transcript order.
func (m *Model) applyDynamicToolItem(item *protocol.ThreadItem, completed bool) {
	if m == nil || item == nil || m.State == nil {
		return
	}
	callID := strings.TrimSpace(firstNonEmpty(item.CallID, item.ID))
	if callID == "" {
		return
	}
	status := strings.TrimSpace(item.Status)
	if completed && status == "" {
		if item.Success != nil && !*item.Success {
			status = "failed"
		} else {
			status = "completed"
		}
	}
	cell := historycell.DynamicToolCallCell{
		CallID:    callID,
		Name:      firstNonEmpty(item.ToolName, item.Tool),
		Arguments: dynamicToolArgumentsText(item.Arguments),
		Status:    status,
		Output:    dynamicToolOutputLines(item),
	}
	if m.dynamicToolMessageIndex == nil {
		m.dynamicToolMessageIndex = map[string]int{}
	}
	width := max(m.width, 20)
	message := historyMessageFromCell(cell, width)
	if index, ok := m.dynamicToolMessageIndex[callID]; ok && index >= 0 && index < len(m.State.Messages) {
		m.State.Messages[index] = message
		m.State.BumpMessagesRevision()
	} else {
		m.State.AddHistoryMessage(message)
		m.dynamicToolMessageIndex[callID] = len(m.State.Messages) - 1
	}
	m.refreshTranscript()
}

func dynamicToolArgumentsText(value *any) string {
	if value == nil {
		return ""
	}
	switch typed := (*value).(type) {
	case string:
		return strings.TrimSpace(typed)
	case nil:
		return ""
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func dynamicToolOutputLines(item *protocol.ThreadItem) []string {
	if item == nil {
		return nil
	}
	var output []string
	for _, value := range []string{item.Output, item.Text} {
		if value = strings.TrimSpace(value); value != "" {
			output = append(output, value)
		}
	}
	for _, value := range item.Content {
		if value = strings.TrimSpace(value); value != "" {
			output = append(output, value)
		}
	}
	return output
}

// registerAgentsOverviewThreadMetadata registers a root task immediately and
// folds child metadata into the matching root row. Existing rows keep their
// current metadata, and removed-thread tombstones win over delayed responses.
func (m *Model) registerAgentsOverviewThreadMetadata(thread *appserver.Thread) {
	if m == nil || m.agentsOverview == nil || thread == nil {
		return
	}
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" || m.agentsOverviewRemoved[threadID] {
		return
	}
	parentID := ""
	if thread.ParentThreadID != nil {
		parentID = strings.TrimSpace(*thread.ParentThreadID)
	}
	for index := range m.agentsOverview.Rows {
		row := &m.agentsOverview.Rows[index]
		if row.ThreadID == threadID {
			return
		}
		if parentID != "" && row.ThreadID == parentID {
			row.ChildThreadIDs = append(row.ChildThreadIDs, threadID)
			m.markAgentsOverviewVoiceOwners(m.agentsOverview.Rows)
			return
		}
	}
	if parentID != "" {
		// The root row is not loaded yet; the next refresh will fold the child.
		return
	}
	rows := append([]agentsoverview.Row(nil), m.agentsOverview.Rows...)
	rows = append(rows, agentsOverviewRowFromThread(thread))
	m.agentsOverview.ApplyRefresh(rows, m.agentsOverview.SelectedThreadID())
	m.markAgentsOverviewVoiceOwners(m.agentsOverview.Rows)
}

func agentsOverviewRowFromThread(thread *appserver.Thread) agentsoverview.Row {
	row := agentsoverview.Row{
		ThreadID:  strings.TrimSpace(thread.ID),
		Preview:   strings.TrimSpace(thread.Preview),
		CWD:       strings.TrimSpace(thread.CWD),
		UpdatedAt: thread.UpdatedAt,
	}
	waitingApproval := false
	waitingUserInput := false
	for _, flag := range thread.Status.ActiveFlags {
		switch flag {
		case appserver.ThreadActiveFlagWaitingOnApproval:
			waitingApproval = true
		case appserver.ThreadActiveFlagWaitingOnUserInput:
			waitingUserInput = true
		}
	}
	row.Group = agentsoverview.GroupForStatus(thread.Status.Type, waitingApproval, waitingUserInput)
	row.StatusType = strings.TrimSpace(thread.Status.Type)
	row.StatusActive = strings.EqualFold(strings.TrimSpace(thread.Status.Type), "active")
	if thread.Name != nil {
		row.Name = strings.TrimSpace(*thread.Name)
	}
	if thread.Model != nil {
		row.Model = strings.TrimSpace(*thread.Model)
	}
	if thread.ReasoningEffort != nil {
		row.ReasoningEffort = strings.TrimSpace(string(*thread.ReasoningEffort))
	}
	if thread.GitInfo != nil && thread.GitInfo.Branch != nil {
		row.GitBranch = strings.TrimSpace(*thread.GitInfo.Branch)
	}
	return row
}

// IsDynamicToolThread reports whether a task was started through the TUI's
// dynamic tools.
func (m *Model) IsDynamicToolThread(threadID string) bool {
	if m == nil || m.dynamicToolThreads == nil {
		return false
	}
	return m.dynamicToolThreads[strings.TrimSpace(threadID)]
}
