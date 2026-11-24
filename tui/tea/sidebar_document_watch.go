package tea

import (
	"fmt"
	"os"
	"strings"
	"time"

	bubbletea "charm.land/bubbletea/v2"
)

// Rust parity: codex-rs/tui/src/app/sidebar_document/watch.rs (#62583).
//
// A preview shows the file as it was when the tab loaded. The pane re-reads a
// previewed file when its stamp changes, so an editor's atomic save shows the
// new content at the same reading position, a deleted or unreadable file
// reports the failure, and the preview recovers when the file comes back.

// sidebarDocumentWatchInterval is Rust's three-second poll cadence for a
// preview whose watch could not be registered.
const sidebarDocumentWatchInterval = 3 * time.Second

// sidebarDocumentWatchTickMsg asks the pane to look for changed previews.
type sidebarDocumentWatchTickMsg struct{}

// sidebarDocumentStamp reports the stamp the current preview content was read
// at. A failed stat is its own stamp so a missing file still refreshes once and
// a deleted-then-restored file refreshes again.
func (m *Model) sidebarDocumentStamp(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if m != nil && m.onSidebarDocumentStat != nil {
		stamp, err := m.onSidebarDocumentStat(path)
		if err != nil {
			return "error:" + err.Error()
		}
		return stamp
	}
	info, err := os.Stat(path)
	if err != nil {
		return "error:" + err.Error()
	}
	return fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
}

// sidebarDocumentWatchCmd arms the next refresh sweep while a preview is open.
// At most one sweep is armed, so a load that arrives mid-sweep cannot stack a
// second timer chain.
func (m *Model) sidebarDocumentWatchCmd() bubbletea.Cmd {
	if m == nil || m.sidebarDocumentWatchScheduled || len(m.sidebarDocumentTabs()) == 0 {
		return nil
	}
	m.sidebarDocumentWatchScheduled = true
	return bubbletea.Tick(sidebarDocumentWatchInterval, func(time.Time) bubbletea.Msg {
		return sidebarDocumentWatchTickMsg{}
	})
}

// applySidebarDocumentWatchTick re-reads every preview whose file changed since
// its last load and re-arms the sweep.
func (m *Model) applySidebarDocumentWatchTick() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.sidebarDocumentWatchScheduled = false
	cmds := m.sidebarDocumentReloadChangedCmds()
	next := m.sidebarDocumentWatchCmd()
	if len(cmds) == 0 {
		return next
	}
	if next != nil {
		cmds = append(cmds, next)
	}
	return bubbletea.Batch(cmds...)
}

// sidebarDocumentReloadChangedCmds re-reads every preview whose file changed
// since its last load. The reload reports through SidebarDocumentLoadedMsg,
// which keeps the tab's scroll position (Rust watch.rs load()).
func (m *Model) sidebarDocumentReloadChangedCmds() []bubbletea.Cmd {
	if m == nil {
		return nil
	}
	loader := m.onSidebarDocument
	if loader == nil {
		return nil
	}
	cmds := make([]bubbletea.Cmd, 0, 2)
	for _, tab := range m.sidebarDocumentTabs() {
		if tab == nil || tab.Snapshot || tab.AwaitingOpen || tab.Loading || strings.TrimSpace(tab.Path) == "" {
			continue
		}
		stamp := m.sidebarDocumentStamp(tab.Path)
		if stamp == tab.watchStamp {
			continue
		}
		tab.watchStamp = stamp
		parent, source, id := tab.Parent, tab.Source, tab.ID
		request := SidebarDocumentRequest{
			ParentThreadID: parent,
			SourceThreadID: source,
			Path:           tab.Path,
			Link:           tab.Link,
			Location:       tab.Location,
		}
		cmds = append(cmds, func() bubbletea.Msg {
			return SidebarDocumentLoadedMsg{ParentThreadID: parent, ID: id, View: loader(request)}
		})
	}
	return cmds
}
