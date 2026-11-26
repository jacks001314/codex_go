package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	"codex_go/tui/markdown"
	mousemsg "codex_go/tui/mousemsg"
)

// Rust parity: codex-rs/tui/src/transcript_view (TerminalHyperlink::document,
// TextLayout::link_at) and markdown_render::local_links::sidebar_file_link
// (#61880). A Markdown link that names a local `.md` / `.markdown` file is a
// document target: an enabled workspace resolves it to the reader instead of
// handing the path to the terminal.
//
// Go paints transcript links as `file://` OSC-8 escapes, so the target is read
// back from the painted row the same way saved-conversation links are. The
// escape stays in the output, which keeps the file link usable from the
// terminal; Ctrl-click, the Go carrier for Rust's modifier-click, prefers the
// in-app preview.

// regionDocumentLink records one painted document target on a rendered row.
type regionDocumentLink struct {
	Start int
	End   int
	Path  string
}

// documentLinkRanges maps each painted row to the document targets it carries.
func documentLinkRanges(section string) map[int][]regionDocumentLink {
	if !strings.Contains(section, ".md") && !strings.Contains(section, ".markdown") {
		return nil
	}
	rows := strings.Split(strings.ReplaceAll(section, "\r\n", "\n"), "\n")
	var links map[int][]regionDocumentLink
	for row, line := range rows {
		for _, link := range codextui.OSC8LinksInLine(line) {
			path, ok := markdown.DocumentLinkTarget(link.Destination)
			if !ok {
				continue
			}
			if links == nil {
				links = map[int][]regionDocumentLink{}
			}
			links[row] = append(links[row], regionDocumentLink{Start: link.Start, End: link.End, Path: path})
		}
	}
	return links
}

// filterRegionDocuments keeps only targets whose row survived into the painted
// text, mirroring filterRegionLinks for saved-conversation links.
func filterRegionDocuments(links map[int][]regionDocumentLink, height int) map[int][]regionDocumentLink {
	if len(links) == 0 {
		return nil
	}
	filtered := make(map[int][]regionDocumentLink, len(links))
	for row, entries := range links {
		if row < 0 || row >= height {
			continue
		}
		filtered[row] = entries
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

// documentLinkAt resolves the document target painted under a cell.
func (m *Model) documentLinkAt(x, y int) (string, bool) {
	if m == nil {
		return "", false
	}
	region, ok := m.transcriptSelectionRegionAt(x, y)
	if !ok {
		return "", false
	}
	for _, link := range region.documents[y-region.y] {
		if x-region.x >= link.Start && x-region.x < link.End {
			return link.Path, true
		}
	}
	return "", false
}

// handleDocumentLinkClick opens a document target in the workspace reader.
// Previews are optional: without a loader the gesture stays with the terminal,
// which still owns the `file://` escape.
func (m *Model) handleDocumentLinkClick(msg mousemsg.MouseMsg) (bool, bubbletea.Cmd) {
	if m == nil || m.onSidebarDocument == nil || !msg.Ctrl ||
		msg.Action != mousemsg.MouseActionPress ||
		msg.Button != mousemsg.MouseButtonLeft {
		return false, nil
	}
	path, ok := m.documentLinkAt(msg.X, msg.Y)
	if !ok {
		return false, nil
	}
	return true, m.openSidebarDocumentAt(path, -1, nil)
}
