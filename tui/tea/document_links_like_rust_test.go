package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	mousemsg "codex_go/tui/mousemsg"
)

// documentLinkModel paints one assistant message that links to a local file and
// installs the workspace reader so a document target can be resolved.
func documentLinkModel(t *testing.T, target string, withReader bool) (*Model, *[]SidebarDocumentRequest) {
	t.Helper()
	model, _ := newSidebarTabsModel(t)
	requests := &[]SidebarDocumentRequest{}
	if withReader {
		model.onSidebarDocument = func(request SidebarDocumentRequest) SidebarDocumentView {
			*requests = append(*requests, request)
			return SidebarDocumentView{Path: request.Path, Text: "# Guide\n"}
		}
	}
	model.State.AddMessage(codextui.RoleAssistant, "Open [Guide]("+target+") before answering.")
	model.refreshTranscript()
	_ = model.viewString()
	return model, requests
}

// ctrlClickTranscriptColumn presses Ctrl on the painted cell that shows needle.
func ctrlClickTranscriptColumn(t *testing.T, model *Model, needle string) bubbletea.Cmd {
	t.Helper()
	region := model.transcriptSelectionRegion
	if region.w <= 0 || region.h <= 0 {
		t.Fatalf("transcript region not registered: %#v", region)
	}
	for row, line := range region.text {
		column := findDisplayColumn(line, needle)
		if column < 0 {
			continue
		}
		_, cmd := model.Update(mousemsg.MouseMsg{
			X:      region.x + column,
			Y:      region.y + row,
			Action: mousemsg.MouseActionPress,
			Button: mousemsg.MouseButtonLeft,
			Ctrl:   true,
		})
		return cmd
	}
	t.Fatalf("painted text %q not found in %#v", needle, region.text)
	return nil
}

// Rust #61880: with the reader available, a local `.md` link in the transcript
// resolves to the workspace document preview.
func TestTranscriptDocumentLinkOpensPreviewLikeRust(t *testing.T) {
	model, requests := documentLinkModel(t, "/workspace/docs/guide.md", true)
	cmd := ctrlClickTranscriptColumn(t, model, "/workspace/docs/guide.md")
	if cmd == nil {
		t.Fatal("Ctrl-click on a document link did not open the reader")
	}
	runTeaCmd(t, model, cmd)
	if len(*requests) != 1 || (*requests)[0].Path != "/workspace/docs/guide.md" {
		t.Fatalf("document requests = %#v", *requests)
	}
	if (*requests)[0].Link != -1 || (*requests)[0].Location != nil {
		t.Fatalf("request kept a preview link/coordinate: %#v", (*requests)[0])
	}
}

// Rust #61880: only `.md`/`.markdown` targets are documents. A source link keeps
// the terminal's own file hyperlink and never opens the reader.
func TestTranscriptSourceLinkStaysWithTerminalLikeRust(t *testing.T) {
	model, requests := documentLinkModel(t, "/workspace/docs/main.go", true)
	if cmd := ctrlClickTranscriptColumn(t, model, "/workspace/docs/main.go"); cmd != nil {
		t.Fatalf("source link produced an in-app command")
	}
	if len(*requests) != 0 {
		t.Fatalf("source link opened a preview: %#v", *requests)
	}
	if len(model.transcriptSelectionRegion.documents) != 0 {
		t.Fatalf("source link recorded as a document target: %#v", model.transcriptSelectionRegion.documents)
	}
}

// Rust #61880: without a reader the document target is not claimed by the TUI;
// the terminal still owns the painted file hyperlink.
func TestTranscriptDocumentLinkWithoutReaderIsNotClaimed(t *testing.T) {
	model, requests := documentLinkModel(t, "/workspace/docs/guide.md", false)
	if cmd := ctrlClickTranscriptColumn(t, model, "/workspace/docs/guide.md"); cmd != nil {
		t.Fatalf("document link produced a command without a reader")
	}
	if len(*requests) != 0 {
		t.Fatalf("requests = %#v", *requests)
	}
	if len(model.transcriptSelectionRegion.documents) == 0 {
		t.Fatal("painted document target was not recorded")
	}
}
