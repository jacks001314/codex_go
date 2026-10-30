package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// SessionPickerItemsFunc lists the resume picker's sessions from the backend
// this TUI is connected to (Rust #58280 BackendSessions::picker_views). The
// notice mirrors interactiveRemoteSessionPickerItems' partial-load message.
type SessionPickerItemsFunc func() (items []codextui.SessionSummary, notice string, err error)

// SessionPickerRefreshMsg reports a live listing loaded while the picker is
// open. PickerID scopes the result to the modal that requested it.
type SessionPickerRefreshMsg struct {
	PickerID string
	Items    []codextui.SessionSummary
	Notice   string
	Err      error
}

// sessionPickerRefreshCmd lists sessions through the connected backend so an
// in-app picker sees sessions created after startup (Rust #58280).
func (m *Model) sessionPickerRefreshCmd(pickerID string) bubbletea.Cmd {
	if m == nil || m.onListSessionPickerItems == nil {
		return nil
	}
	list := m.onListSessionPickerItems
	return func() bubbletea.Msg {
		items, notice, err := list()
		return SessionPickerRefreshMsg{PickerID: pickerID, Items: items, Notice: notice, Err: err}
	}
}

// applySessionPickerRefresh merges a live listing into the open picker while
// preserving the selected row and any pending page-down distance (Rust #58280
// levels the picker's rows on the live backend listing).
func (m *Model) applySessionPickerRefresh(msg SessionPickerRefreshMsg) {
	if m == nil || m.modal == nil || m.modal.sessionPicker == nil {
		return
	}
	if strings.TrimSpace(msg.PickerID) != "" && strings.TrimSpace(m.modal.id) != strings.TrimSpace(msg.PickerID) {
		return
	}
	m.modal.sessionPicker.Loading = false
	if strings.TrimSpace(msg.Notice) != "" {
		m.modal.sessionPickerNotice = strings.TrimSpace(msg.Notice)
	}
	if msg.Err != nil {
		if m.sessionPickerLoadNotice == "" {
			m.modal.sessionPicker.Error = strings.TrimSpace(msg.Err.Error())
		}
		return
	}
	m.sessionItems = mergeSessionPickerItems(m.sessionItems, msg.Items)
	m.modal.sessionPicker.IngestItems(msg.Items)
}

// mergeSessionPickerItems appends unseen rows and refreshes the unread receipt
// of existing ones, matching Rust's ingest_page + update_unread ordering.
func mergeSessionPickerItems(existing []codextui.SessionSummary, incoming []codextui.SessionSummary) []codextui.SessionSummary {
	if len(incoming) == 0 {
		return existing
	}
	index := make(map[string]int, len(existing))
	for i := range existing {
		index[sessionPickerItemKey(existing[i])] = i
	}
	for _, item := range incoming {
		key := sessionPickerItemKey(item)
		if i, ok := index[key]; ok && key != "" {
			existing[i].UnreadRevision = strings.TrimSpace(item.UnreadRevision)
			continue
		}
		if key != "" {
			index[key] = len(existing)
		}
		existing = append(existing, item)
	}
	return existing
}

// sessionPickerItemKey mirrors SessionSummary rows that identify by path when
// they carry no thread id.
func sessionPickerItemKey(item codextui.SessionSummary) string {
	if threadID := strings.TrimSpace(item.ThreadID); threadID != "" {
		return "thread\x00" + item.BackendID.String() + "\x00" + threadID
	}
	if path := strings.TrimSpace(item.Path); path != "" {
		return "path\x00" + item.BackendID.String() + "\x00" + path
	}
	return ""
}
