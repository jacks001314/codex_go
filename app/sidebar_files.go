package app

import (
	"os"
	"path/filepath"
	"strings"

	"codex_go/appserver"
	"codex_go/tui/chatwidget"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/app/sidebar_files.rs `Files` (#61920).
//
// The inventory keeps the files a conversation mentions in first-seen order and
// merges later pages without moving the confirmed window. Reconnects drop the
// incremental window so the next page fetch can re-scan from the history cursor.

type sidebarFiles struct {
	entries            []markdown.SidebarFileLink
	byDestination      map[string]int
	confirmed          int
	cwd                string
	inspectLocalPaths  bool
	next               *string
	needsHistoryCursor bool
	seenCursors        map[string]bool
	olderTurn          []appserver.ThreadItemEntry
	reviewMode         bool
}

func newSidebarFiles(cwd string, next *string, inspectLocalPaths bool) *sidebarFiles {
	return &sidebarFiles{
		byDestination:     map[string]int{},
		cwd:               strings.TrimSpace(cwd),
		inspectLocalPaths: inspectLocalPaths,
		next:              cloneStringPtrForSidebar(next),
		seenCursors:       map[string]bool{},
	}
}

// connectionReplaced forgets the incremental scan window. Callers must restore
// `next` from the thread's older-item cursor before the next page.
func (f *sidebarFiles) connectionReplaced() {
	f.needsHistoryCursor = true
	f.confirmed = 0
	f.seenCursors = map[string]bool{}
	f.olderTurn = nil
	f.reviewMode = false
}

// applyPage ingests one items page, grouping the scan by turn so review-mode and
// voice-handoff filtering sees whole turns (Rust `apply_page`).
func (f *sidebarFiles) applyPage(page appserver.ThreadItemsListResponse) bool {
	if cursor := f.next; cursor != nil {
		f.seenCursors[strings.TrimSpace(*cursor)] = true
	}
	f.next = nil
	if page.NextCursor != nil && !f.seenCursors[strings.TrimSpace(*page.NextCursor)] {
		f.next = cloneStringPtrForSidebar(page.NextCursor)
	}
	var entries []markdown.SidebarFileLink
	for _, entry := range page.Data {
		if len(f.olderTurn) > 0 && f.olderTurn[len(f.olderTurn)-1].TurnID != entry.TurnID {
			f.finishOlderTurn(&entries)
		}
		if sidebarFilesScansItem(entry.Item) {
			f.olderTurn = append(f.olderTurn, entry)
		}
	}
	if f.next == nil {
		f.finishOlderTurn(&entries)
	}
	return f.extendPage(entries)
}

func (f *sidebarFiles) finishOlderTurn(entries *[]markdown.SidebarFileLink) {
	if len(f.olderTurn) == 0 {
		return
	}
	handoff := -1
	for index := len(f.olderTurn) - 1; index >= 0; index-- {
		if sidebarItemIsVoiceHandoff(f.olderTurn[index].Item) {
			handoff = index
			break
		}
	}
	for index, entry := range f.olderTurn {
		item := entry.Item
		switch remoteTUINormalizedThreadItemType(item.Type) {
		case "exitedreviewmode":
			f.reviewMode = true
		case "enteredreviewmode":
			f.reviewMode = false
		case "usermessage":
			if f.reviewMode {
				continue
			}
			if handoff >= 0 && index < handoff && sidebarPrivateRealtimeItem(item) {
				continue
			}
			*entries = append(*entries, sidebarNewestItemFileLinks(f.cwd, item)...)
		default:
			if handoff >= 0 && index < handoff && sidebarPrivateRealtimeItem(item) {
				continue
			}
			*entries = append(*entries, sidebarNewestItemFileLinks(f.cwd, item)...)
		}
	}
	f.olderTurn = nil
}

func (f *sidebarFiles) extendPage(entries []markdown.SidebarFileLink) bool {
	if f.confirmed == len(f.entries) {
		changed := f.extend(entries)
		f.confirmed = len(f.entries)
		return changed
	}
	return f.mergeHistory(f.confirmed, entries)
}

// extend adds entries, keeping the first path order and filling in a location
// that a later mention provided (Rust `extend`).
func (f *sidebarFiles) extend(entries []markdown.SidebarFileLink) bool {
	changed := false
	for _, entry := range entries {
		if index, ok := f.byDestination[entry.Path]; ok {
			if f.entries[index].Location == nil && entry.Location != nil {
				f.entries[index].Location = entry.Location
				changed = true
			}
			continue
		}
		if f.inspectLocalPaths && filepath.IsAbs(entry.Path) && sidebarPathIsDir(entry.Path) {
			continue
		}
		f.byDestination[entry.Path] = len(f.entries)
		f.entries = append(f.entries, entry)
		changed = true
	}
	return changed
}

// mergeHistory inserts promoted or re-scanned entries after the first `start`
// confirmed entries and re-appends the previous tail (Rust `merge_history`).
func (f *sidebarFiles) mergeHistory(start int, entries []markdown.SidebarFileLink) bool {
	if start > len(f.entries) {
		start = len(f.entries)
	}
	previous := append([]markdown.SidebarFileLink(nil), f.entries[start:]...)
	confirmed := f.confirmed - start
	if confirmed < 0 {
		confirmed = 0
	}
	f.entries = f.entries[:start]
	for destination, index := range f.byDestination {
		if index >= start {
			delete(f.byDestination, destination)
		}
	}
	f.extend(entries)
	for offset, old := range previous {
		if offset == confirmed {
			f.confirmed = len(f.entries)
		}
		if index, ok := f.byDestination[old.Path]; ok {
			if f.entries[index].Location == nil {
				f.entries[index].Location = old.Location
			}
			continue
		}
		f.byDestination[old.Path] = len(f.entries)
		f.entries = append(f.entries, old)
	}
	if confirmed == len(previous) {
		f.confirmed = len(f.entries)
	}
	return !sidebarFileLinksEqual(f.entries[start:], previous)
}

func (f *sidebarFiles) promote(entries []markdown.SidebarFileLink) bool {
	return f.mergeHistory(0, entries)
}

func sidebarFilesScansItem(item appserver.ThreadItem) bool {
	switch remoteTUINormalizedThreadItemType(item.Type) {
	case "agentmessage", "usermessage", "plan", "filechange", "imagegeneration",
		"enteredreviewmode", "exitedreviewmode":
		return true
	}
	return false
}

func sidebarItemIsVoiceHandoff(item appserver.ThreadItem) bool {
	return sidebarUserMessageIsDelegation(item)
}

func sidebarPrivateRealtimeItem(item appserver.ThreadItem) bool {
	phase := strings.TrimSpace(sidebarStringFromAny(item.Data["phase"]))
	if phase == "" {
		phase = strings.TrimSpace(sidebarStringFromAny(item.Data["messagePhase"]))
	}
	return chatwidget.IsPrivateRealtimeAgentItem(item.Type, phase, item.Text)
}

func sidebarPathIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func sidebarFileLinksEqual(left, right []markdown.SidebarFileLink) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Path != right[index].Path {
			return false
		}
		leftLocation, rightLocation := left[index].Location, right[index].Location
		if (leftLocation == nil) != (rightLocation == nil) {
			return false
		}
		if leftLocation != nil && *leftLocation != *rightLocation {
			return false
		}
	}
	return true
}

func cloneStringPtrForSidebar(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
