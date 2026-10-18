package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #47847: a copy is rejected while another clipboard write is in flight,
// and a stale completion cannot clear the newer request.
func TestClipboardCopyRejectsConcurrentWritesLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		OnClipboardWrite: func(string) error { return nil },
	})
	first := model.clipboardWriteCmd("first", "one", false, false)
	if !model.clipboardBusy || model.pendingClipboardRequest == 0 {
		t.Fatalf("first copy did not become busy: %#v", model)
	}
	second := model.clipboardWriteCmd("second", "two", false, false)
	if busy, ok := second().(ClipboardBusyMsg); !ok || busy.Label != "second" {
		t.Fatalf("concurrent copy = %#v, want ClipboardBusyMsg", second())
	}

	firstMsg := first().(ClipboardWriteResultMsg)
	model.Update(firstMsg)
	if model.clipboardBusy || model.pendingClipboardRequest != 0 {
		t.Fatalf("completed copy left the worker busy: %#v", model)
	}

	third := model.clipboardWriteCmd("third", "three", false, false)
	thirdMsg := third().(ClipboardWriteResultMsg)
	if thirdMsg.RequestID == 0 || thirdMsg.RequestID == firstMsg.RequestID {
		t.Fatalf("request ids did not advance: first=%d third=%d", firstMsg.RequestID, thirdMsg.RequestID)
	}
	// A stale result must not clear the newer in-flight request.
	model.Update(firstMsg)
	if !model.clipboardBusy {
		t.Fatal("stale clipboard result cleared the newer request")
	}
	model.Update(thirdMsg)
	if model.clipboardBusy {
		t.Fatal("current clipboard result did not clear busy")
	}
}

func TestClipboardBusyNoticeLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{})
	model.Update(ClipboardBusyMsg{Label: "copy"})
	if !strings.Contains(model.notice, "Copy already in progress") {
		t.Fatalf("busy notice = %q", model.notice)
	}
}
