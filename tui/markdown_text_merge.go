package tui

// Rust parity: codex-rs/tui/src/markdown_text_merge.rs.

type MarkdownEventKind int

const (
	MarkdownEventText MarkdownEventKind = iota
	MarkdownEventOther
)

type SourceRange struct {
	Start int
	End   int
}

type MarkdownTextEvent struct {
	Kind  MarkdownEventKind
	Text  string
	Range SourceRange
}

func MergeAdjacentTextEvents(events []MarkdownTextEvent) []MarkdownTextEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]MarkdownTextEvent, 0, len(events))
	for _, event := range events {
		if event.Kind == MarkdownEventText && len(out) > 0 && out[len(out)-1].Kind == MarkdownEventText {
			out[len(out)-1].Text += event.Text
			out[len(out)-1].Range.End = event.Range.End
			continue
		}
		out = append(out, event)
	}
	return out
}
