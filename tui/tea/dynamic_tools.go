package tea

import (
	"strings"
	"sync/atomic"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
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
	// Refresh the open dashboard so the dispatched task appears; Rust also marks
	// it as dispatched before the next list refresh.
	return m.refreshAgentsOverviewCmd()
}

// IsDynamicToolThread reports whether a task was started through the TUI's
// dynamic tools.
func (m *Model) IsDynamicToolThread(threadID string) bool {
	if m == nil || m.dynamicToolThreads == nil {
		return false
	}
	return m.dynamicToolThreads[strings.TrimSpace(threadID)]
}
