package state

import (
	"reflect"
	"testing"
	"time"
)

func TestTurnStatePendingAndMailbox(t *testing.T) {
	state := NewTurnState()
	state.InsertPendingApproval("approval", 1)
	state.pendingRequestPermissions["perm"] = 2
	if state.PendingWaiterCount() != 2 {
		t.Fatalf("pending count = %d", state.PendingWaiterCount())
	}
	if state.RemovePendingApproval("approval") != 1 {
		t.Fatal("approval not removed")
	}
	state.ClearPendingWaiters()
	if state.PendingWaiterCount() != 0 {
		t.Fatalf("pending count after clear = %d", state.PendingWaiterCount())
	}
	if !state.AcceptsMailboxDeliveryForCurrentTurn() {
		t.Fatal("default should accept mailbox for current turn")
	}
	state.SetMailboxDeliveryPhase(MailboxNextTurn)
	if state.AcceptsMailboxDeliveryForCurrentTurn() {
		t.Fatal("next turn phase should not accept current delivery")
	}
	state.AcceptMailboxDeliveryForCurrentTurn()
	if !state.AcceptsMailboxDeliveryForCurrentTurn() {
		t.Fatal("current phase not restored")
	}
}

func TestTurnStatePermissionsAndCounters(t *testing.T) {
	state := NewTurnState()
	state.RecordGrantedPermissions("env", map[string]any{"read": true})
	state.RecordGrantedPermissions("env", map[string]any{"write": false})
	permissions := state.GrantedPermissions("env")
	if permissions["read"] != true || permissions["write"] != false {
		t.Fatalf("permissions = %#v", permissions)
	}
	state.SetStrictAutoReviewEnabled(true)
	if !state.StrictAutoReviewEnabled() {
		t.Fatal("strict auto review not enabled")
	}
	if state.IncrementToolCalls() != 1 || state.ToolCalls() != 1 {
		t.Fatalf("tool calls = %d", state.ToolCalls())
	}
	state.RecordMemoryCitation()
	if !state.HasMemoryCitation() {
		t.Fatal("memory citation not recorded")
	}
}

func TestSessionStateHistoryAndFirstTurn(t *testing.T) {
	state := NewSessionState("thread-a")
	state.RecordItems("a", "b")
	if !reflect.DeepEqual(state.CloneHistory(), []any{"a", "b"}) {
		t.Fatalf("history = %#v", state.CloneHistory())
	}
	state.AutoCompactWindow().SetEstimatedPrefill(10)
	state.ReplaceHistory([]any{"c"})
	if !reflect.DeepEqual(state.CloneHistory(), []any{"c"}) {
		t.Fatalf("history = %#v", state.CloneHistory())
	}
	if state.AutoCompactWindow().Snapshot().PrefillInputTokens != nil {
		t.Fatal("replace history should clear prefill")
	}
	if !state.TakeNextTurnIsFirst() || state.TakeNextTurnIsFirst() {
		t.Fatal("first turn flag should be consumed")
	}
	state.SetNextTurnIsFirst(true)
	if !state.TakeNextTurnIsFirst() {
		t.Fatal("first turn flag not restored")
	}
}

func TestSessionStateSelectionsAndPermissions(t *testing.T) {
	state := NewSessionState("thread-a")
	state.RecordMCPDependencyPrompted("b", "a")
	if !reflect.DeepEqual(state.MCPDependencyPrompted(), []string{"a", "b"}) {
		t.Fatalf("prompted = %#v", state.MCPDependencyPrompted())
	}
	if !reflect.DeepEqual(state.MergeConnectorSelection("c", "a"), []string{"a", "c"}) {
		t.Fatalf("connectors = %#v", state.MergeConnectorSelection())
	}
	state.SetRateLimits(map[string]any{"requests": 10})
	state.SetRateLimits(map[string]any{"tokens": 20})
	limits := state.RateLimits()
	if limits["requests"] != 10 || limits["tokens"] != 20 {
		t.Fatalf("limits = %#v", limits)
	}
	state.RecordGrantedPermissions("env", map[string]any{"network": true})
	if state.GrantedPermissions("env")["network"] != true {
		t.Fatalf("permissions = %#v", state.GrantedPermissions("env"))
	}
}

func TestServicesActiveTaskAndTerminals(t *testing.T) {
	services := NewServices("thread-a")
	services.StartTask("task-a", TaskRegular)
	active := services.ActiveTurn()
	if active.Task == nil || active.Task.ID != "task-a" {
		t.Fatalf("active = %+v", active)
	}
	if err := services.FinishTask("wrong"); err == nil {
		t.Fatal("expected active task mismatch")
	}
	if err := services.FinishTask("task-a"); err != nil {
		t.Fatalf("finish task: %v", err)
	}
	if services.ActiveTurn().Task != nil {
		t.Fatal("active task not cleared")
	}

	started := fixedTime()
	services.RecordTerminal(BackgroundTerminalInfo{ID: "term-a", Command: []string{"go", "test"}, CWD: "/repo", StartedAt: started})
	if len(services.ListTerminals(false)) != 1 {
		t.Fatal("terminal not recorded")
	}
	exited := started.Add(time.Second)
	if !services.FinishTerminal("term-a", 0, exited) {
		t.Fatal("terminal not finished")
	}
	if len(services.ListTerminals(false)) != 0 {
		t.Fatal("exited terminal should be hidden")
	}
	if len(services.ListTerminals(true)) != 1 {
		t.Fatal("exited terminal should be listed when requested")
	}
}

func TestServicesExposeSubservices(t *testing.T) {
	services := NewServices("thread-a")
	if services.Session() == nil || services.Tasks() == nil || services.Metrics() == nil || services.GuardianStore() == nil || services.GuardianBreaker() == nil {
		t.Fatal("subservices not initialized")
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
}
