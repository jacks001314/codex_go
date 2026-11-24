package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

func runSidebarOpenCmd(cmd bubbletea.Cmd) {
	if cmd != nil {
		_ = cmd()
	}
}

func newSidebarOpenModel(t *testing.T) (*Model, *[]SidebarDocumentRequest) {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	model := NewModel(state, Options{Width: 120, Height: 40})
	requests := &[]SidebarDocumentRequest{}
	model.onSidebarDocument = func(request SidebarDocumentRequest) SidebarDocumentView {
		*requests = append(*requests, request)
		return SidebarDocumentView{Path: request.Path, Text: "package main\n", Code: true, LineStarts: []int{0}}
	}
	return model, requests
}

// Rust #63184: the model's request opens a preview tab for the visible thread
// without taking keyboard focus from the chat.
func TestSidebarOpenRequestOpensVisiblyLikeRust(t *testing.T) {
	model, requests := newSidebarOpenModel(t)
	model.sidebarChooserFocused = false
	runSidebarOpenCmd(model.applySidebarOpenRequest(SidebarOpenRequestMsg{SourceThreadID: "thread-a", Path: "src/main.go", Line: 3}))
	if len(*requests) != 1 {
		t.Fatalf("document requests = %#v", *requests)
	}
	request := (*requests)[0]
	if request.SourceThreadID != "thread-a" || request.Path != "src/main.go" {
		t.Fatalf("request = %#v", request)
	}
	if request.Location == nil || request.Location.Line != 3 {
		t.Fatalf("location = %#v", request.Location)
	}
	tabs := model.sidebarDocumentsFor("thread-a")
	if len(tabs) != 1 || tabs[0].Path != "src/main.go" || !tabs[0].Loading {
		t.Fatalf("tabs = %#v", tabs)
	}
	if model.sidebarDocumentActiveTab() != tabs[0] {
		t.Fatal("the requested document is not the active tab")
	}
	if model.sidebarChooserFocused {
		t.Fatal("opening a model preview took keyboard focus from the chat")
	}
}

// A request for another conversation waits until that conversation is visible.
func TestSidebarOpenRequestQueuesForOtherThreadLikeRust(t *testing.T) {
	model, requests := newSidebarOpenModel(t)
	model.applySidebarOpenRequest(SidebarOpenRequestMsg{SourceThreadID: "thread-b", Path: "docs/readme.md"})
	if len(*requests) != 0 {
		t.Fatalf("document read before the calling thread was visible: %#v", *requests)
	}
	if len(model.sidebarDocumentsFor("thread-b")) != 0 {
		t.Fatal("queued request opened a tab on an invisible thread")
	}
	model.switchThreadID("thread-b")
	runSidebarOpenCmd(model.drainPendingSidebarOpens())
	if len(*requests) != 1 || (*requests)[0].SourceThreadID != "thread-b" {
		t.Fatalf("drained requests = %#v", *requests)
	}
	if tabs := model.sidebarDocumentsFor("thread-b"); len(tabs) != 1 || tabs[0].Path != "docs/readme.md" {
		t.Fatalf("tabs = %#v", tabs)
	}
}

// The tool may only show files for the conversation that asked, and blank
// paths are ignored.
func TestSidebarOpenRequestRequiresCallingThreadLikeRust(t *testing.T) {
	model, requests := newSidebarOpenModel(t)
	model.applySidebarOpenRequest(SidebarOpenRequestMsg{Path: "src/main.go"})
	model.applySidebarOpenRequest(SidebarOpenRequestMsg{SourceThreadID: "thread-a", Path: "  "})
	if len(*requests) != 0 {
		t.Fatalf("requests = %#v", *requests)
	}
	if strings.TrimSpace(model.currentThreadID()) != "thread-a" {
		t.Fatalf("thread = %q", model.currentThreadID())
	}
}
