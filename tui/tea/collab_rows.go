package tea

import "codex_go/protocol"

// collabTranscriptRow remembers one painted collab tool call: its item and the
// raw text of the history message it produced. Rust #63444 relabels such rows
// when a subagent nickname lookup completes after the row was drawn.
type collabTranscriptRow struct {
	item      protocol.ThreadItem
	completed bool
	raw       string
	// missing names the receivers still waiting for a nickname. Rust #63444
	// repaints a row only once one of them resolves.
	missing []string
}
