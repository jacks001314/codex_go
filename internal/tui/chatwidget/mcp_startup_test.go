package chatwidget

import (
	"reflect"
	"strings"
	"testing"
)

func TestMcpStartupFinishAfterLagWithoutActiveStatusDoesNotEnterIgnoreModeMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState([]string{"alpha", "beta"})

	lag := state.FinishAfterLag()
	if lag.Finished || state.IgnoreUpdatesUntilNextStart {
		t.Fatalf("finish after lag without active status = %#v state=%#v", lag, state)
	}

	failed := state.Update("alpha", McpStartupStatus{Kind: McpStartupFailed, Error: "alpha failed"}, true)
	if !failed.Active || !reflect.DeepEqual(failed.Warnings, []string{"alpha failed"}) {
		t.Fatalf("alpha failed update = %#v", failed)
	}
	if failed.Finished {
		t.Fatalf("startup should wait for beta, got %#v", failed)
	}

	finished := state.Update("beta", McpStartupStatus{Kind: McpStartupReady}, true)
	if !finished.Finished || !reflect.DeepEqual(finished.Failed, []string{"alpha"}) {
		t.Fatalf("settled update = %#v", finished)
	}
	if got := strings.Join(finished.Warnings, "\n"); !strings.Contains(got, "MCP startup incomplete (failed: alpha)") {
		t.Fatalf("warnings = %#v", finished.Warnings)
	}
}

func TestMcpStartupIgnoreModeRequiresExpectedServersMatchRust(t *testing.T) {
	state := McpStartupRoundState{
		IgnoreUpdatesUntilNextStart: true,
		PendingNextRound:            map[string]McpStartupStatus{},
	}

	result := state.Update("runtime", McpStartupStatus{Kind: McpStartupStarting}, true)
	if result.Active || result.Header != "" || len(state.Status) != 0 {
		t.Fatalf("unexpected activation without expected servers: result=%#v state=%#v", result, state)
	}
}

func TestMcpStartupEmptyExpectedServersReactivatesNextRoundMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState(nil)
	state.SetExpectedServers(nil)
	state.Finish()

	started := state.Update("runtime", McpStartupStatus{Kind: McpStartupStarting}, true)
	if !started.Active || started.Header != MCPStartupSingleHeaderPrefix+" runtime" {
		t.Fatalf("started = %#v", started)
	}

	finished := state.Update("runtime", McpStartupStatus{Kind: McpStartupFailed, Error: "runtime failed"}, true)
	if !finished.Finished || !reflect.DeepEqual(finished.Failed, []string{"runtime"}) {
		t.Fatalf("finished = %#v", finished)
	}
	if got := strings.Join(finished.Warnings, "\n"); !strings.Contains(got, "runtime failed") || !strings.Contains(got, "MCP startup incomplete (failed: runtime)") {
		t.Fatalf("warnings = %#v", finished.Warnings)
	}
}

func TestMcpStartupAfterLagTerminalOnlyNextRoundMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState([]string{"alpha", "beta"})
	state.Update("alpha", McpStartupStatus{Kind: McpStartupStarting}, true)
	state.Update("alpha", McpStartupStatus{Kind: McpStartupFailed, Error: "alpha failed"}, true)
	state.Update("beta", McpStartupStatus{Kind: McpStartupStarting}, true)

	firstFinish := state.FinishAfterLag()
	if !firstFinish.Finished || !reflect.DeepEqual(firstFinish.Failed, []string{"alpha"}) || !reflect.DeepEqual(firstFinish.Cancelled, []string{"beta"}) {
		t.Fatalf("first finish = %#v", firstFinish)
	}

	lateAlpha := state.Update("alpha", McpStartupStatus{Kind: McpStartupFailed, Error: "stale alpha failed"}, true)
	if lateAlpha.Active || len(lateAlpha.Warnings) != 0 {
		t.Fatalf("late alpha should stay buffered: %#v", lateAlpha)
	}
	state.FinishAfterLag()

	next := state.Update("beta", McpStartupStatus{Kind: McpStartupReady}, true)
	if !next.Finished || !reflect.DeepEqual(next.Failed, []string{"alpha"}) {
		t.Fatalf("terminal-only next round = %#v", next)
	}
	if got := strings.Join(next.Warnings, "\n"); !strings.Contains(got, "stale alpha failed") || !strings.Contains(got, "MCP startup incomplete (failed: alpha)") {
		t.Fatalf("warnings = %#v", next.Warnings)
	}
}

func TestMcpStartupPreservesServerAndErrorTextMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState([]string{" alpha "})
	started := state.Update(" alpha ", McpStartupStatus{Kind: McpStartupStarting}, true)
	if started.Header != MCPStartupSingleHeaderPrefix+"  alpha " {
		t.Fatalf("header should preserve server text, got %q", started.Header)
	}

	finished := state.Update(" alpha ", McpStartupStatus{Kind: McpStartupFailed, Error: "  boom  "}, true)
	if !reflect.DeepEqual(finished.Warnings[:1], []string{"  boom  "}) {
		t.Fatalf("warnings should preserve error text: %#v", finished.Warnings)
	}
}
