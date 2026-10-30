package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

func TestSessionPickerRefreshesFromConnectedBackendLikeRust(t *testing.T) {
	listed := 0
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-1", Preview: "old", CWD: "/repo"}},
		OnListSessionPickerItems: func() ([]codextui.SessionSummary, string, error) {
			listed++
			return []codextui.SessionSummary{
				{ThreadID: "thread-1", Preview: "old", CWD: "/repo", UnreadRevision: "revision-2"},
				{ThreadID: "thread-2", Preview: "new", CWD: "/repo"},
			}, "", nil
		},
	})
	typeText(t, model, "/resume")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("resume picker not open: %#v", model.modal)
	}
	runTeaCmd(t, model, cmd)
	if listed != 1 {
		t.Fatalf("live listing calls = %d, want 1", listed)
	}
	byID := map[string]codextui.SessionSummary{}
	for _, item := range model.modal.sessionPicker.Items {
		byID[item.ThreadID] = item
	}
	if got := byID["thread-2"]; got.Preview != "new" {
		t.Fatalf("live session missing from the open picker: %#v", model.modal.sessionPicker.Items)
	}
	if got := byID["thread-1"]; got.UnreadRevision != "revision-2" {
		t.Fatalf("existing row receipt = %q, want the live revision", got.UnreadRevision)
	}
	if len(model.sessionItems) != 2 {
		t.Fatalf("session snapshot = %#v, want the live listing merged in", model.sessionItems)
	}
	rows := strings.Join(model.modal.sessionPicker.RenderRows(80, time.Now()), "\n")
	if !strings.Contains(rows, "new") || !strings.Contains(rows, sessionUnreadMarkerSGR) {
		t.Fatalf("refreshed picker rows = %q", rows)
	}
}

func TestSessionPickerOpensForLiveListingWhenSnapshotIsEmptyLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:             80,
		Height:            24,
		DisablePasteBurst: true,
		SessionPickerCWD:  "/repo",
		OnListSessionPickerItems: func() ([]codextui.SessionSummary, string, error) {
			return []codextui.SessionSummary{{ThreadID: "thread-1", Preview: "fresh", CWD: "/repo"}}, "", nil
		},
	})
	typeText(t, model, "/resume")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if model.modal == nil || model.modal.sessionPicker == nil {
		t.Fatalf("picker should open while the live listing loads: %#v", model.modal)
	}
	runTeaCmd(t, model, cmd)
	rows := strings.Join(model.modal.sessionPicker.RenderRows(80, time.Now()), "\n")
	if !strings.Contains(rows, "fresh") {
		t.Fatalf("live-only session missing from the picker: %q", rows)
	}
}

func TestSessionPickerIgnoresRefreshAfterCloseLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:              80,
		Height:             24,
		DisablePasteBurst:  true,
		SessionPickerCWD:   "/repo",
		SessionPickerItems: []codextui.SessionSummary{{ThreadID: "thread-1", Preview: "old", CWD: "/repo"}},
		OnListSessionPickerItems: func() ([]codextui.SessionSummary, string, error) {
			return nil, "", nil
		},
	})
	typeText(t, model, "/resume")
	model.Update(key(bubbletea.KeyEnter))
	model.modal = nil
	model.Update(SessionPickerRefreshMsg{
		PickerID: "session-picker-resume",
		Items:    []codextui.SessionSummary{{ThreadID: "thread-2", Preview: "late", CWD: "/repo"}},
	})
	if len(model.sessionItems) != 1 || model.sessionItems[0].ThreadID != "thread-1" {
		t.Fatalf("closed picker ingested a late listing: %#v", model.sessionItems)
	}
}
