package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

// Rust #62149 sidebar_agent_status::State::label: every phase carries a glyph
// and a caption the chooser paints next to the agent name.
func TestSidebarAgentPhaseLabelsLikeRust(t *testing.T) {
	cases := []struct {
		phase  sidebarAgentPhase
		symbol string
		status string
	}{
		{sidebarAgentStarting, "●", "Starting"},
		{sidebarAgentRunning, "●", "Running"},
		{sidebarAgentWaiting, "●", "Waiting"},
		{sidebarAgentIdle, "○", "Idle"},
		{sidebarAgentCompleted, "○", "Completed"},
		{sidebarAgentStopped, "○", "Stopped"},
		{sidebarAgentFailed, "!", "Failed"},
		{sidebarAgentClosed, "○", "Closed"},
		{sidebarAgentUnavailable, "○", "Unavailable"},
	}
	for _, tc := range cases {
		symbol, status := tc.phase.label()
		if symbol != tc.symbol || status != tc.status {
			t.Fatalf("label(%d) = %q/%q, want %q/%q", tc.phase, symbol, status, tc.symbol, tc.status)
		}
	}
	if !sidebarAgentRunning.active() || sidebarAgentCompleted.active() {
		t.Fatal("active() must cover only starting/running/waiting")
	}
	if !sidebarAgentFailed.done() || sidebarAgentIdle.done() {
		t.Fatal("done() must cover only explicit outcomes")
	}
}

// Rust State::supersedes: spawn activity can arrive after a completion and must
// not clear the outcome, while a fresh explicit start may take it over.
func TestSidebarAgentOutcomeSupersedesLikeRust(t *testing.T) {
	cases := []struct {
		name      string
		next      sidebarAgentPhase
		startHint bool
		want      bool
	}{
		{"idle after completion", sidebarAgentIdle, false, false},
		{"unavailable after completion", sidebarAgentUnavailable, false, false},
		{"spawn hint after completion", sidebarAgentRunning, true, false},
		{"completion over completion", sidebarAgentCompleted, false, true},
		{"running after idle", sidebarAgentRunning, false, true},
	}
	for _, tc := range cases {
		if got := tc.next.supersedes(sidebarAgentCompleted, true, tc.startHint); got != tc.want {
			t.Fatalf("%s: supersedes = %t, want %t", tc.name, got, tc.want)
		}
	}
	if !sidebarAgentRunning.supersedes(sidebarAgentIdle, true, false) {
		t.Fatal("idle must not block a later running update")
	}
}

// Rust Overview::chooser_rows: the Subagents section lists spawn-tree members,
// active agents first, then explicit outcomes, then idle ones, and names prefer
// the agent path without its /root/ prefix.
func TestSidebarAgentChooserSectionLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	seed := []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "agent-idle", ParentThreadID: "parent-a", AgentNickname: "Quiet", AgentRole: "worker"},
		{ThreadID: "agent-done", ParentThreadID: "parent-a"},
		{ThreadID: "agent-busy", ParentThreadID: "parent-a", AgentPath: "/root/review/worker"},
	}
	model.mergeSidebarAgentEntries("parent-a", seed)
	model.setSidebarAgentPhase("parent-a", "agent-done", sidebarAgentCompleted, false)
	model.setSidebarAgentPhase("parent-a", "agent-busy", sidebarAgentRunning, false)
	openChooserWithFiles(t, model, nil, "")
	model.transcript.Height = 20

	rows := model.sidebarAgentRows()
	if len(rows) != 3 {
		t.Fatalf("agent rows = %d, want 3", len(rows))
	}
	// Rust sorts by (!active, done): working agents first, then idle ones, and
	// explicit outcomes last so they stay visible at the section's end.
	wantOrder := []string{"agent-busy", "agent-idle", "agent-done"}
	for index, want := range wantOrder {
		if rows[index].ThreadID != want {
			t.Fatalf("row %d = %q, want %q", index, rows[index].ThreadID, want)
		}
	}
	if rows[0].Name != "review/worker" || rows[0].Symbol != "●" || rows[0].Status != "Running" {
		t.Fatalf("busy row = %+v", rows[0])
	}
	if rows[1].Name != "Quiet [worker]" || rows[1].Status != "Idle" {
		t.Fatalf("idle row = %+v, want nickname and role", rows[1])
	}
	if rows[2].Status != "Completed" || rows[2].Name != "agent-done" {
		t.Fatalf("completed row = %+v", rows[2])
	}

	rendered := strings.Split(ansi.Strip(model.renderSidebarChooser(80, 20)), "\n")
	header := ""
	for _, line := range rendered {
		if strings.Contains(line, sidebarSubagentsGroupTitle) {
			header = line
			break
		}
	}
	if header != " Subagents  3" {
		t.Fatalf("subagents header = %q", header)
	}
	found := 0
	for _, line := range rendered {
		if strings.Contains(line, "review/worker") && strings.Contains(line, "Running") {
			found++
		}
		if strings.Contains(line, "Quiet [worker]") && strings.Contains(line, "Idle") {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("painted agent rows = %d, want 2 in:\n%s", found, strings.Join(rendered, "\n"))
	}
	if start := model.sidebarChooserAgentStartFor(model.sidebarChooserChoices()); start != 1 {
		t.Fatalf("agent start = %d, want the row after New side chat", start)
	}
	if got := model.sidebarChooserFileStart(); got != 4 {
		t.Fatalf("file start = %d, want agents pushed the files down", got)
	}
	if _, ok := model.sidebarChooserAgent(model.sidebarChooserAgentIndex("agent-done")); !ok {
		t.Fatal("agent index does not map back to a Subagents row")
	}
}

// Rust paint: a narrow pane gives the status its own indented row under the
// agent name.
func TestSidebarChooserNarrowAgentRowsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	model.mergeSidebarAgentEntries("parent-a", []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "agent-busy", ParentThreadID: "parent-a", AgentPath: "/root/review/worker"},
	})
	model.setSidebarAgentPhase("parent-a", "agent-busy", sidebarAgentRunning, false)
	openChooserWithFiles(t, model, nil, "")

	rendered := strings.Split(ansi.Strip(model.renderSidebarChooser(40, 20)), "\n")
	joined := strings.Join(rendered, "\n")
	if !strings.Contains(joined, "review/worker") || !strings.Contains(joined, "    Running") {
		t.Fatalf("narrow agent rows missing name or status:\n%s", joined)
	}
}

// Rust App::observe_sidebar_agent_event: a spawn tool call registers the child
// and its status, and a later completion keeps the outcome through repeated
// idle status updates.
func TestSidebarAgentLiveSpawnAndOutcomeLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	receivers := []string{"agent-child"}
	states := map[string]protocol.CollabAgentState{"agent-child": {Status: "running"}}
	model.observeSidebarAgentItem("parent-a", &protocol.ThreadItem{
		Type:              "collab_tool_call",
		Tool:              "spawn_agent",
		ReceiverThreadIDs: &receivers,
		AgentsStates:      &states,
	})
	rows := model.sidebarAgentRows()
	if len(rows) != 1 || rows[0].ThreadID != "agent-child" || rows[0].Status != "Running" {
		t.Fatalf("spawn did not register a running agent: %+v", rows)
	}

	completed := map[string]protocol.CollabAgentState{"agent-child": {Status: "completed"}}
	model.observeSidebarAgentItem("parent-a", &protocol.ThreadItem{
		Type:         "collab_tool_call",
		Tool:         "wait",
		AgentsStates: &completed,
	})
	model.observeSidebarAgentItem("parent-a", &protocol.ThreadItem{
		Type:          "sub_agent_activity",
		ActivityKind:  "started",
		AgentThreadID: "agent-child",
		AgentPath:     "/root/reviewer",
	})
	if rows := model.sidebarAgentRows(); rows[0].Status != "Completed" {
		t.Fatalf("late spawn activity cleared the outcome: %+v", rows[0])
	}
	if rows := model.sidebarAgentRows(); rows[0].Name != "reviewer" {
		t.Fatalf("spawn activity did not name the agent: %+v", rows[0])
	}
}

// Rust App::restore_sidebar_agent_outcomes / seed_discovered_sidebar_agent: a
// discovery read registers descendants parent-first and drops unrelated
// threads.
func TestSidebarAgentDiscoveryRegistersDescendantsLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	entries := []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "grandchild", ParentThreadID: "child", AgentNickname: "Deep"},
		{ThreadID: "child", ParentThreadID: "parent-a", AgentRole: "reviewer", IsRunning: true},
		{ThreadID: "unrelated", ParentThreadID: "other-root", AgentNickname: "Stranger"},
	}
	model.sidebarAgentRequest++
	model.applySidebarAgentsLoaded(SidebarAgentsLoadedMsg{
		ParentThreadID: "parent-a",
		RequestID:      model.sidebarAgentRequest,
		Entries:        entries,
	})
	rows := model.sidebarAgentRows()
	if len(rows) != 2 {
		t.Fatalf("agent rows = %d, want the two descendants: %+v", len(rows), rows)
	}
	statuses := map[string]string{}
	names := map[string]string{}
	for _, row := range rows {
		statuses[row.ThreadID] = row.Status
		names[row.ThreadID] = row.Name
	}
	if statuses["child"] != "Running" || statuses["grandchild"] != "Idle" {
		t.Fatalf("discovered statuses = %v", statuses)
	}
	if names["child"] != "[reviewer]" || names["grandchild"] != "Deep" {
		t.Fatalf("discovered names = %v", names)
	}
}

// Rust side_focus: accepting a Subagents row switches to that conversation.
func TestSidebarChooserAgentAcceptSwitchesThreadLikeRust(t *testing.T) {
	model, _ := newSidebarTabsModel(t)
	var switched []string
	model.onSwitchAgent = func(threadID string) (AgentThreadSwitchResponse, error) {
		switched = append(switched, threadID)
		return AgentThreadSwitchResponse{Entry: codextui.AgentThreadEntry{ThreadID: threadID}, Status: "idle"}, nil
	}
	model.mergeSidebarAgentEntries("parent-a", []codextui.AgentThreadEntry{
		{ThreadID: "parent-a", IsPrimary: true},
		{ThreadID: "agent-b", ParentThreadID: "parent-a", AgentNickname: "Sage"},
	})
	openChooserWithFiles(t, model, nil, "")

	model.sidebarChoice = model.sidebarChooserAgentIndex("agent-b")
	if model.sidebarChoice <= 0 {
		t.Fatalf("agent row index = %d", model.sidebarChoice)
	}
	cmd := model.acceptSidebarChooser()
	if model.sidebarChooserOpen {
		t.Fatal("accepting an agent row left the chooser open")
	}
	runTeaCmdTree(t, model, cmd)
	if len(switched) != 1 || switched[0] != "agent-b" {
		t.Fatalf("switched threads = %v, want agent-b", switched)
	}
}

// Rust footer/row_targets: with a Subagents section present the chooser offers
// movement and accept even before the first main-chat turn.
func TestSidebarChooserAgentSectionReachableBeforeFirstTurnLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("parent-empty")
	model := NewModel(state, Options{Width: 120, Height: 30})
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.mergeSidebarAgentEntries("parent-empty", []codextui.AgentThreadEntry{
		{ThreadID: "parent-empty", IsPrimary: true},
		{ThreadID: "agent-b", ParentThreadID: "parent-empty", IsRunning: true},
	})
	model.sidebarChooserOpen = true
	model.sidebarChooserParent = "parent-empty"
	model.sidebarChooserFocused = true
	model.refreshTranscript()

	if model.sidebarChatCreationAvailable() {
		t.Fatal("test needs a parent without a first turn")
	}
	truthy, _ := model.updateSidebarChooserKey(key(keymsg.KeyDown))
	if !truthy {
		t.Fatal("the chooser ignored list keys with only Subagents rows")
	}
	footer := ansi.Strip(model.sidebarChooserFooter(sidebarChooserBodyWidth(80)))
	if !strings.Contains(footer, "move") {
		t.Fatalf("footer = %q, want the move hint", footer)
	}
	if !strings.Contains(footer, "open") {
		t.Fatalf("footer = %q, want accept reachable on agent rows", footer)
	}
}
