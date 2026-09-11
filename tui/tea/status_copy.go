package tea

import (
	"strings"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// statusCopyTargets captures the /status output so /copy can offer the whole
// status and its individual fields until another command, user message, or
// assistant response supersedes it (Rust #43055).
type statusCopyTargets struct {
	snapshot codextui.State
	width    int
}

func newStatusCopyTargets(snapshot codextui.State, width int) *statusCopyTargets {
	return &statusCopyTargets{snapshot: snapshot, width: width}
}

// withSnapshot refreshes the retained snapshot so whole-status copies include
// refreshed rate limits (Rust #43055).
func (t *statusCopyTargets) withSnapshot(snapshot codextui.State) {
	if t == nil {
		return
	}
	t.snapshot = snapshot
}

// targets rebuilds the copy options at copy time.
func (t *statusCopyTargets) targets() []chatwidget.CopyTarget {
	if t == nil {
		return nil
	}
	out := []chatwidget.CopyTarget{{
		ID:          statusCopyWholeID,
		Label:       "Whole status",
		Text:        statusHistoryText(t.snapshot.RenderStatusCardWidth(t.width)),
		Description: "Copy the entire status output as plain text.",
	}}
	if model := strings.TrimSpace(t.snapshot.Model); model != "" {
		out = append(out, chatwidget.CopyTarget{
			ID:          "status-model",
			Label:       "Model",
			Text:        model,
			Description: "Copy the model name.",
		})
	}
	if cwd := strings.TrimSpace(t.snapshot.CWD); cwd != "" {
		out = append(out, chatwidget.CopyTarget{
			ID:          "status-directory",
			Label:       "Directory",
			Text:        cwd,
			Description: "Copy the full working directory path.",
		})
	}
	if name := strings.TrimSpace(t.snapshot.ThreadName); name != "" {
		out = append(out, chatwidget.CopyTarget{
			ID:          "status-thread-name",
			Label:       "Thread name",
			Text:        name,
			Description: "Copy the thread name.",
		})
	}
	if id := strings.TrimSpace(t.snapshot.ThreadID); id != "" {
		out = append(out, chatwidget.CopyTarget{
			ID:          "status-session-id",
			Label:       "Session ID",
			Text:        id,
			Description: "Copy the session id.",
		})
	}
	return out
}

// statusCopyWholeID is the whole-status picker id. It differs from the response
// whole-target id so status copies stay plain text (Rust #43055).
const statusCopyWholeID = "whole-status"
