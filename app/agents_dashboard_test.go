package app

import (
	"context"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/session"
	agentsoverview "codex_go/tui/agents_overview"
)

func dashboardThreadRows() []*appserver.Thread {
	idle := appserver.IdleStatus()
	active := appserver.ThreadStatus{Type: "active", ActiveFlags: []appserver.ThreadActiveFlag{}}
	waiting := appserver.ThreadStatus{Type: "active", ActiveFlags: []appserver.ThreadActiveFlag{appserver.ThreadActiveFlagWaitingOnApproval}}
	notLoaded := appserver.NotLoadedStatus()
	branch := "main"
	parent := "root-1"
	name := "alpha"
	return []*appserver.Thread{
		{ID: "root-1", Name: &name, Preview: "fix parser", CWD: "/work/a", Status: active, GitInfo: &appserver.GitInfo{Branch: &branch}},
		{ID: "root-2", Preview: "idle task", CWD: "/work/b", Status: idle},
		{ID: "root-3", Preview: "needs approval", CWD: "/work/c", Status: waiting},
		{ID: "sub-1", ParentThreadID: &parent, Preview: "subagent", CWD: "/work/a", Status: active},
		{ID: "root-4", Preview: "finished", CWD: "/work/d", Status: notLoaded},
	}
}

func TestAgentsOverviewRowsFromThreadsLikeRust(t *testing.T) {
	rows := agentsOverviewRowsFromThreads(dashboardThreadRows(), "root-1")
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (subagent folded)", len(rows))
	}
	byID := map[string]agentsoverview.Row{}
	for _, row := range rows {
		byID[row.ThreadID] = row
	}
	root1, ok := byID["root-1"]
	if !ok {
		t.Fatal("missing root-1")
	}
	if !root1.IsCurrent || !root1.StatusActive || root1.Group != agentsoverview.GroupWorking {
		t.Fatalf("root-1 row = %#v", root1)
	}
	if root1.Name != "alpha" || root1.GitBranch != "main" || root1.CWD != "/work/a" {
		t.Fatalf("root-1 fields = %#v", root1)
	}
	if byID["root-2"].Group != agentsoverview.GroupReady || byID["root-2"].StatusActive {
		t.Fatalf("root-2 row = %#v", byID["root-2"])
	}
	if byID["root-3"].Group != agentsoverview.GroupNeedsYou {
		t.Fatalf("root-3 group = %v, want NeedsYou", byID["root-3"].Group)
	}
	if byID["root-4"].Group != agentsoverview.GroupFinished {
		t.Fatalf("root-4 group = %v, want Finished", byID["root-4"].Group)
	}
}

func dashboardRecords() []session.Record {
	branch := "feature"
	inProgress := "inprogress"
	failed := "error"
	return []session.Record{
		{ID: "r-1", Title: "beta", Metadata: session.Metadata{CWD: "/work/b", Git: map[string]string{"branch": branch}, RolloutTurns: []session.TurnSnapshot{{ID: "t-1", Status: inProgress}}}},
		{ID: "r-2", Title: "gamma", Metadata: session.Metadata{CWD: "/work/c", RolloutTurns: []session.TurnSnapshot{{ID: "t-2", Status: failed}}}},
		{ID: "r-3", Title: "delta", Metadata: session.Metadata{CWD: "/work/d"}},
		{ID: "r-4", Title: "archived", Archived: true, Metadata: session.Metadata{CWD: "/work/e"}},
		{ID: "r-5", Title: "sub", ParentThreadID: "r-1", Metadata: session.Metadata{CWD: "/work/b"}},
	}
}

func TestAgentsOverviewRowsFromRecordsLikeRust(t *testing.T) {
	rows := agentsOverviewRowsFromRecords(dashboardRecords(), "")
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (subagent excluded)", len(rows))
	}
	byID := map[string]agentsoverview.Row{}
	for _, row := range rows {
		byID[row.ThreadID] = row
	}
	if byID["r-1"].Group != agentsoverview.GroupWorking || !byID["r-1"].StatusActive {
		t.Fatalf("r-1 row = %#v", byID["r-1"])
	}
	if byID["r-1"].GitBranch != "feature" {
		t.Fatalf("r-1 branch = %q", byID["r-1"].GitBranch)
	}
	if byID["r-2"].Group != agentsoverview.GroupNeedsYou {
		t.Fatalf("r-2 group = %v, want NeedsYou", byID["r-2"].Group)
	}
	if byID["r-3"].Group != agentsoverview.GroupReady {
		t.Fatalf("r-3 group = %v, want Ready", byID["r-3"].Group)
	}
	if byID["r-4"].Group != agentsoverview.GroupFinished {
		t.Fatalf("r-4 group = %v, want Finished", byID["r-4"].Group)
	}
	if _, exists := byID["r-5"]; exists {
		t.Fatal("subagent record r-5 should be excluded")
	}
}

func TestLocalAgentGroupForRecord(t *testing.T) {
	archived := &session.Record{Archived: true}
	if group, active := localAgentGroupForRecord(archived); group != agentsoverview.GroupFinished || active {
		t.Fatalf("archived = %v/%v", group, active)
	}
	empty := &session.Record{}
	if group, _ := localAgentGroupForRecord(empty); group != agentsoverview.GroupReady {
		t.Fatalf("empty = %v, want Ready", group)
	}
}

type fakeAgentsDashboardSource struct {
	rows          []agentsoverview.Row
	listErr       error
	dispatched    []string
	dispatchedCwd string
	stopped       []string
	renamed       map[string]string
	dispatchErr   error
}

func (s *fakeAgentsDashboardSource) List(ctx context.Context) ([]agentsoverview.Row, error) {
	return s.rows, s.listErr
}

func (s *fakeAgentsDashboardSource) Dispatch(ctx context.Context, prompt, cwd string) (string, error) {
	s.dispatched = append(s.dispatched, prompt)
	s.dispatchedCwd = cwd
	return "new-1", s.dispatchErr
}

func (s *fakeAgentsDashboardSource) Stop(ctx context.Context, threadID string) error {
	s.stopped = append(s.stopped, threadID)
	return nil
}

func (s *fakeAgentsDashboardSource) Rename(ctx context.Context, threadID, name string) error {
	if s.renamed == nil {
		s.renamed = map[string]string{}
	}
	s.renamed[threadID] = name
	return nil
}

func (s *fakeAgentsDashboardSource) Close() {}

func newFakeDashboardSource() *fakeAgentsDashboardSource {
	return &fakeAgentsDashboardSource{rows: []agentsoverview.Row{
		{ThreadID: "root-1", Name: "alpha", Preview: "fix parser", CWD: "/work/a", Group: agentsoverview.GroupWorking, StatusActive: true},
		{ThreadID: "root-2", Name: "beta", Preview: "idle", CWD: "/work/a", Group: agentsoverview.GroupReady},
	}}
}

func keyRunes(runes ...rune) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: runes}
}

func keyPress(keyType bubbletea.KeyType) bubbletea.KeyMsg {
	return bubbletea.KeyMsg{Type: keyType}
}

func TestAgentsDashboardLoadsAndRenders(t *testing.T) {
	source := newFakeDashboardSource()
	model := newAgentsDashboardModel(context.Background(), source)
	command := model.Init()
	if command == nil {
		t.Fatal("Init returned no refresh command")
	}
	if _, command := model.Update(bubbletea.WindowSizeMsg{Width: 120, Height: 24}); command != nil {
		t.Fatal("WindowSizeMsg returned a command")
	}
	message := command()
	updated, _ := model.Update(message)
	finished, ok := updated.(*agentsDashboardModel)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	output := finished.View()
	for _, want := range []string{"Agent command center", "1 working   1 ready", "alpha", "beta"} {
		if !strings.Contains(output, want) {
			t.Errorf("view missing %q:\n%s", want, output)
		}
	}
}

func TestAgentsDashboardDispatchTask(t *testing.T) {
	source := newFakeDashboardSource()
	model := newAgentsDashboardModel(context.Background(), source)
	model.view.ApplyRefresh(source.rows, "")

	// Type "add tests" and press enter -> dispatch with selected project cwd.
	for _, r := range "add tests" {
		model.Update(keyRunes(r))
	}
	updated, command := model.Update(keyPress(bubbletea.KeyEnter))
	if command == nil {
		t.Fatal("enter returned no dispatch command")
	}
	message := command()
	updated, _ = updated.Update(message)
	finished := updated.(*agentsDashboardModel)
	if len(source.dispatched) != 1 || source.dispatched[0] != "add tests" {
		t.Fatalf("dispatched = %v", source.dispatched)
	}
	if source.dispatchedCwd != "/work/a" {
		t.Fatalf("dispatch cwd = %q, want selected project cwd /work/a", source.dispatchedCwd)
	}
	if !strings.Contains(finished.View(), "Dispatched task new-1") {
		t.Fatalf("notice missing dispatch confirmation:\n%s", finished.View())
	}
}

func TestAgentsDashboardOpenSelectedThread(t *testing.T) {
	source := newFakeDashboardSource()
	model := newAgentsDashboardModel(context.Background(), source)
	model.view.ApplyRefresh(source.rows, "")
	model.view.Selected = 1
	updated, command := model.Update(keyPress(bubbletea.KeyEnter))
	if command == nil {
		t.Fatal("enter returned no quit command")
	}
	if _, ok := command().(bubbletea.QuitMsg); !ok {
		t.Fatalf("enter command did not produce QuitMsg")
	}
	finished := updated.(*agentsDashboardModel)
	if !finished.done || finished.result == nil || finished.result.OpenedThreadID != "root-2" {
		t.Fatalf("open result = %#v done=%v", finished.result, finished.done)
	}
}

func TestAgentsDashboardExitOnEsc(t *testing.T) {
	source := newFakeDashboardSource()
	model := newAgentsDashboardModel(context.Background(), source)
	model.view.ApplyRefresh(source.rows, "")
	updated, command := model.Update(keyPress(bubbletea.KeyEsc))
	if command == nil {
		t.Fatal("esc returned no quit command")
	}
	if _, ok := command().(bubbletea.QuitMsg); !ok {
		t.Fatalf("esc command did not produce QuitMsg")
	}
	if !updated.(*agentsDashboardModel).done {
		t.Fatal("esc did not finish the dashboard")
	}
}

func TestAgentsDashboardStopSelected(t *testing.T) {
	source := newFakeDashboardSource()
	model := newAgentsDashboardModel(context.Background(), source)
	model.view.ApplyRefresh(source.rows, "")
	model.view.Selected = 0 // root-1 active
	updated, command := model.Update(keyPress(bubbletea.KeyCtrlX))
	if command == nil {
		t.Fatal("ctrl+x returned no stop command")
	}
	message := command()
	updated, _ = updated.Update(message)
	finished := updated.(*agentsDashboardModel)
	_ = finished
	if len(source.stopped) != 1 || source.stopped[0] != "root-1" {
		t.Fatalf("stopped = %v, want [root-1]", source.stopped)
	}
}

func TestAgentsDashboardRenameSelected(t *testing.T) {
	source := newFakeDashboardSource()
	model := newAgentsDashboardModel(context.Background(), source)
	model.view.ApplyRefresh(source.rows, "")
	model.view.Selected = 1
	model.Update(keyPress(bubbletea.KeyCtrlR))
	if !model.view.State.Renaming {
		t.Fatal("ctrl+r did not start renaming")
	}
	model.Update(keyRunes(' ', 'v', '2'))
	updated, command := model.Update(keyPress(bubbletea.KeyEnter))
	if command == nil {
		t.Fatal("enter returned no rename command")
	}
	message := command()
	updated, _ = updated.Update(message)
	_ = updated
	if got := source.renamed["root-2"]; got != "beta v2" {
		t.Fatalf("renamed = %q, want %q", got, "beta v2")
	}
}

func TestAgentsDashboardSearchAndGrouping(t *testing.T) {
	source := newFakeDashboardSource()
	model := newAgentsDashboardModel(context.Background(), source)
	model.view.ApplyRefresh(source.rows, "")
	model.Update(keyPress(bubbletea.KeyCtrlF))
	if !model.view.State.Searching {
		t.Fatal("ctrl+f did not enter search")
	}
	for _, r := range "idle" {
		model.Update(keyRunes(r))
	}
	if visible := model.view.VisibleIndices(); len(visible) != 1 || model.view.Rows[visible[0]].ThreadID != "root-2" {
		t.Fatalf("search visible = %v", visible)
	}
	model.Update(keyPress(bubbletea.KeyEsc))
	if model.view.State.Searching {
		t.Fatal("esc did not exit search")
	}
	model.Update(keyPress(bubbletea.KeyCtrlS))
	if !model.view.State.StatusGrouping {
		t.Fatal("ctrl+s did not toggle status grouping")
	}
	model.Update(keyPress(bubbletea.KeyCtrlN))
	if !model.view.State.StatusGrouping || model.view.State.Searching || model.view.State.Input != "" {
		t.Fatalf("ctrl+n did not clear: %#v", model.view.State)
	}
}

func TestAgentsDashboardListErrorShowsNotice(t *testing.T) {
	source := newFakeDashboardSource()
	source.listErr = context.DeadlineExceeded
	model := newAgentsDashboardModel(context.Background(), source)
	command := model.Init()
	updated, _ := model.Update(command())
	finished := updated.(*agentsDashboardModel)
	if !strings.Contains(finished.View(), "Failed to load shared agents") {
		t.Fatalf("notice missing error:\n%s", finished.View())
	}
}
