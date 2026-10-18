package tea

import (
	"testing"

	codextui "codex_go/tui"
	agentsoverview "codex_go/tui/agents_overview"
)

// Rust #47382: an open agents overview marks the task that owns the running
// voice conversation, and clears the badge when that conversation is no longer
// running.
func TestAgentsOverviewMarksVoiceOwnerLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 20})
	rows := []agentsoverview.Row{
		{ThreadID: "thread-voice", Name: "Voice owner", Group: agentsoverview.GroupReady},
		{ThreadID: "thread-other", Name: "Other", Group: agentsoverview.GroupReady},
	}
	model.agentsOverview = agentsoverview.New(rows, "", false)
	model.VoiceConversation.BeginVoiceConversation("thread-voice", 1)
	model.applyAgentsOverviewList(agentsOverviewListMsg{rows: rows})
	if !model.agentsOverview.Rows[0].HasVoice {
		t.Fatalf("voice owner row was not marked: %#v", model.agentsOverview.Rows[0])
	}
	if model.agentsOverview.Rows[1].HasVoice {
		t.Fatalf("unrelated row was marked as the voice owner: %#v", model.agentsOverview.Rows[1])
	}

	model.VoiceConversation.ResetVoiceConversation()
	model.applyAgentsOverviewList(agentsOverviewListMsg{rows: rows})
	for _, row := range model.agentsOverview.Rows {
		if row.HasVoice {
			t.Fatalf("voice badge survived voice shutdown: %#v", row)
		}
	}
}

// A child thread owning voice badges the root task that folds it in.
func TestAgentsOverviewMarksChildVoiceOwnerLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 20})
	rows := []agentsoverview.Row{
		{
			ThreadID:       "root-task",
			Name:           "Root task",
			Group:          agentsoverview.GroupReady,
			ChildThreadIDs: []string{"child-voice"},
		},
		{ThreadID: "thread-other", Name: "Other", Group: agentsoverview.GroupReady},
	}
	model.agentsOverview = agentsoverview.New(rows, "", false)
	model.VoiceConversation.BeginVoiceConversation("child-voice", 1)
	model.applyAgentsOverviewList(agentsOverviewListMsg{rows: rows})
	if !model.agentsOverview.Rows[0].HasVoice {
		t.Fatalf("root task was not marked for a child voice owner: %#v", model.agentsOverview.Rows[0])
	}
	if model.agentsOverview.Rows[1].HasVoice {
		t.Fatalf("unrelated row was marked as the child voice owner: %#v", model.agentsOverview.Rows[1])
	}
}
