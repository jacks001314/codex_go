package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

func daybreakNoticeModel(t *testing.T, enabled bool) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	return NewModel(state, Options{
		Width:             100,
		Height:            30,
		ShowSessionHeader: true,
		FeatureSettings:   map[string]bool{"cli_daybreak": true},
		DaybreakDefault:   enabled,
	})
}

// Rust #54097: the notice follows the configured session header.
func TestDaybreakNoticeFollowsSessionHeaderLikeRust(t *testing.T) {
	model := daybreakNoticeModel(t, true)
	if len(model.State.Messages) < 2 {
		t.Fatalf("messages = %#v", model.State.Messages)
	}
	header := model.State.Messages[0]
	notice := model.State.Messages[1]
	if header.Role != codextui.RoleHistory || !strings.Contains(header.Text, ">_") {
		t.Fatalf("first message is not the session header: %#v", header)
	}
	if notice.Role != codextui.RoleHistory || !strings.Contains(notice.Text, chatwidget.DaybreakNewTurnsNotice) {
		t.Fatalf("second message is not the Daybreak notice: %#v", notice)
	}
	if count := daybreakNoticeCount(model); count != 1 {
		t.Fatalf("Daybreak notices = %d, want 1", count)
	}

	off := daybreakNoticeModel(t, false)
	if count := daybreakNoticeCount(off); count != 0 {
		t.Fatalf("disabled Daybreak emitted %d notices", count)
	}
}

func TestDaybreakNoticeOnNewThreadAndResumeLikeRust(t *testing.T) {
	model := daybreakNoticeModel(t, true)
	model.startFreshNamedSession("", "new")
	if count := daybreakNoticeCount(model); count != 1 {
		t.Fatalf("/new Daybreak notices = %d, want 1", count)
	}

	resumed := daybreakNoticeModel(t, false)
	resumed.State.Messages = []codextui.Message{{Role: codextui.RoleUser, Text: "older"}}
	resumed.applyResumeResponse("thread-2", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-2", DaybreakEnabled: true},
		Messages: []codextui.Message{{Role: codextui.RoleUser, Text: "replayed"}},
	})
	if len(resumed.State.Messages) < 3 ||
		!strings.Contains(resumed.State.Messages[0].Text, ">_ gcode") ||
		!strings.Contains(resumed.State.Messages[1].Text, chatwidget.DaybreakNewTurnsNotice) ||
		resumed.State.Messages[2].Text != "replayed" {
		t.Fatalf("resumed messages = %#v", resumed.State.Messages)
	}
}

func daybreakNoticeCount(model *Model) int {
	count := 0
	for _, message := range model.State.Messages {
		if message.Role == codextui.RoleHistory &&
			strings.Contains(message.Text, chatwidget.DaybreakNewTurnsNotice) {
			count++
		}
	}
	return count
}
