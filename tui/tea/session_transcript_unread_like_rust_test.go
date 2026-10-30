package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestSessionPickerAcknowledgesUnreadTranscriptLikeRust(t *testing.T) {
	type ack struct{ threadID, revision string }
	var acks []ack
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-1", Title: "One", Preview: "hi", CWD: "/repo", UnreadRevision: "revision-1"}},
		OnReadSessionTranscript: func(threadID string) ([]codextui.Message, error) {
			return []codextui.Message{{Role: codextui.RoleUser, Text: "hello"}}, nil
		},
		OnAcknowledgeSessionRead: func(threadID string, expectedRevision string) (bool, string, error) {
			acks = append(acks, ack{threadID: threadID, revision: expectedRevision})
			return false, "", nil
		},
	})
	openResumePickerForUnreadTest(t, model)
	if rows := strings.Join(model.modal.sessionPicker.RenderRows(80, time.Now()), "\n"); !strings.Contains(rows, sessionUnreadMarkerSGR) {
		t.Fatalf("picker rows lack the unread marker: %q", rows)
	}

	model.Update(key(bubbletea.KeyCtrlT))
	if model.sessionTranscriptRevision != "revision-1" {
		t.Fatalf("observed revision = %q", model.sessionTranscriptRevision)
	}
	_, cmd := model.Update(SessionTranscriptMsg{ThreadID: "thread-1", Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "hello"}}})
	ackMessages := runSessionAckCmd(t, model, cmd)
	if len(ackMessages) != 1 {
		t.Fatalf("ack messages = %#v", ackMessages)
	}
	if len(acks) != 1 || acks[0].threadID != "thread-1" || acks[0].revision != "revision-1" {
		t.Fatalf("acks = %#v", acks)
	}
	if rows := strings.Join(model.modal.sessionPicker.RenderRows(80, time.Now()), "\n"); strings.Contains(rows, sessionUnreadMarkerSGR) {
		t.Fatalf("picker kept the unread marker after acknowledgement: %q", rows)
	}
}

func TestSessionPickerDefersAckUntilTerminalFocusLikeRust(t *testing.T) {
	var acks int
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-1", Preview: "hi", CWD: "/repo", UnreadRevision: "revision-1"}},
		OnAcknowledgeSessionRead: func(threadID string, expectedRevision string) (bool, string, error) {
			acks++
			return false, "", nil
		},
	})
	openResumePickerForUnreadTest(t, model)
	model.terminalFocused = false
	model.Update(key(bubbletea.KeyCtrlT))
	_, cmd := model.Update(SessionTranscriptMsg{ThreadID: "thread-1", Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "hello"}}})
	if ackMessages := runSessionAckCmd(t, model, cmd); len(ackMessages) != 0 || acks != 0 {
		t.Fatalf("ack fired without terminal focus: %#v", ackMessages)
	}

	_, cmd = model.Update(bubbletea.FocusMsg{})
	if ackMessages := runSessionAckCmd(t, model, cmd); len(ackMessages) != 1 || acks != 1 {
		t.Fatalf("ack did not fire on focus: %#v (acks=%d)", ackMessages, acks)
	}
}

func TestSessionPickerUnreadMarkerFollowsAcknowledgementResponseLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-1", Preview: "hi", CWD: "/repo", UnreadRevision: "revision-1"}},
		OnAcknowledgeSessionRead: func(threadID string, expectedRevision string) (bool, string, error) {
			return true, "revision-2", nil
		},
	})
	openResumePickerForUnreadTest(t, model)
	model.Update(key(bubbletea.KeyCtrlT))
	_, cmd := model.Update(SessionTranscriptMsg{ThreadID: "thread-1", Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "hello"}}})
	runSessionAckCmd(t, model, cmd)

	if got := model.modal.sessionPicker.Items[0].UnreadRevision; got != "revision-2" {
		t.Fatalf("unread revision = %q, want the server's newer receipt", got)
	}
	if rows := strings.Join(model.modal.sessionPicker.RenderRows(80, time.Now()), "\n"); !strings.Contains(rows, sessionUnreadMarkerSGR) {
		t.Fatalf("picker dropped a still-unread result: %q", rows)
	}
}

const sessionUnreadMarkerSGR = "\x1b[38;2;59;130;246m\u25cf\x1b[39m"

func openResumePickerForUnreadTest(t *testing.T, model *Model) {
	t.Helper()
	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("resume picker not open: %#v", model.modal)
	}
}

func runSessionAckCmd(t *testing.T, model *Model, cmd bubbletea.Cmd) []SessionTranscriptAcknowledgedMsg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(bubbletea.BatchMsg); ok {
		var acks []SessionTranscriptAcknowledgedMsg
		for _, sub := range batch {
			acks = append(acks, runSessionAckCmd(t, model, sub)...)
		}
		return acks
	}
	if ack, ok := msg.(SessionTranscriptAcknowledgedMsg); ok {
		model.Update(ack)
		return []SessionTranscriptAcknowledgedMsg{ack}
	}
	model.Update(msg)
	return nil
}
