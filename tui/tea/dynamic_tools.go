package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

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
	if m.taskToolThreads == nil {
		m.taskToolThreads = map[string]bool{}
	}
	if message.Available {
		m.taskToolThreads[threadID] = true
		return
	}
	delete(m.taskToolThreads, threadID)
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
	return m.taskToolThreads[threadID]
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
