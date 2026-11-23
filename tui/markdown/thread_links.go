package markdown

import (
	"strings"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/thread_links.rs and markdown_render.rs
// markdown_destination (#62332).
//
// A Markdown link to a saved conversation, for example
// "[Review notes](codex://threads/<id>)", renders as a label the TUI can
// activate. The OSC 8 escape carries the destination for the app's own
// hit-testing; the frame strips it before the terminal sees it so the link
// opens inside the conversation instead of launching the desktop app.

type threadFileLink struct {
	Label    string
	ThreadID string
}

func threadLinkText(threadID string, text string) string {
	return "\x1b]8;;" + codextui.ThreadLinkDestination(threadID) + "\x07" + text + "\x1b]8;;\x07"
}

// annotateThreadLinkLabels wraps the rendered label (or the destination text of
// a label-less link) of each thread link in an app-only hyperlink.
func annotateThreadLinkLabels(rendered string, links []threadFileLink) string {
	if len(links) == 0 {
		return rendered
	}
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	for index, line := range lines {
		for _, link := range links {
			needle := link.Label
			if strings.TrimSpace(needle) == "" {
				needle = codextui.ThreadLinkDestination(link.ThreadID)
			}
			if needle == "" || !strings.Contains(line, needle) {
				continue
			}
			line = strings.ReplaceAll(line, needle, threadLinkText(link.ThreadID, needle))
		}
		lines[index] = line
	}
	return strings.Join(lines, "\n")
}
