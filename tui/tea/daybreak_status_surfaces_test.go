package tea

import (
	"testing"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// TestResumeRestoresDaybreakPreferenceLikeRust covers Rust #49861's session
// flow: the resumed thread's persisted Daybreak preference becomes the live
// value, gated by the cli_daybreak feature, and reaches the status surfaces
// through the status-controls runtime.
func TestResumeRestoresDaybreakPreferenceLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.featureSettings = map[string]bool{"cli_daybreak": true}

	model.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-a", DaybreakEnabled: true},
	})
	if !model.daybreakEnabled {
		t.Fatal("resumed thread with Daybreak enabled kept daybreakEnabled=false")
	}
	if runtime := model.statusControlsRuntime(); !runtime.DaybreakEnabled {
		t.Fatalf("status controls runtime DaybreakEnabled = false, want true")
	}
	model.ensureStatusControls()
	model.syncStatusControlsRuntime()
	value, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineDaybreak)
	if !ok || value != "Daybreak on" {
		t.Fatalf("status line daybreak = %q ok=%v, want Daybreak on", value, ok)
	}

	// A thread without the preference (and any switch to it) clears the value.
	model.applyResumeResponse("thread-b", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-b"},
	})
	if model.daybreakEnabled {
		t.Fatal("resume without a Daybreak preference kept the previous thread's value")
	}

	// With the feature off the preference never turns the item on.
	model.featureSettings = map[string]bool{"cli_daybreak": false}
	model.applyResumeResponse("thread-c", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-c", DaybreakEnabled: true},
	})
	if model.daybreakEnabled {
		t.Fatal("daybreak stayed enabled while the cli_daybreak feature is off")
	}
}

// TestStatusRuntimeFollowsSideConversationLikeRust covers Rust #49861's
// `!side_conversation_active()` guard on the live runtime.
func TestStatusRuntimeFollowsSideConversationLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	model.daybreakEnabled = true
	model.ensureStatusControls()
	model.syncStatusControlsRuntime()
	if runtime := model.statusControls.Runtime; runtime.SideConversationActive {
		t.Fatal("side conversation reported active before one started")
	}
	model.activeSide = &activeSideConversation{ShowingSide: true}
	model.syncStatusControlsRuntime()
	if runtime := model.statusControls.Runtime; !runtime.SideConversationActive {
		t.Fatal("side conversation did not reach the status controls runtime")
	}
	value, ok := model.statusControls.StatusLineValueForItem(bottompane.StatusLineDaybreak)
	if !ok || value != "Daybreak off" {
		t.Fatalf("side-conversation daybreak = %q ok=%v, want Daybreak off", value, ok)
	}
}
