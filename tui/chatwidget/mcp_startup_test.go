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

func TestMcpStartupSettledCanWaitForLagMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState([]string{"alpha", "beta"})
	state.Update("alpha", McpStartupStatus{Kind: McpStartupStarting}, false)
	state.Update("beta", McpStartupStatus{Kind: McpStartupStarting}, false)
	state.Update("alpha", McpStartupStatus{Kind: McpStartupFailed, Error: "alpha failed"}, false)

	settled := state.Update("beta", McpStartupStatus{Kind: McpStartupReady}, false)
	if !settled.Settled || settled.Finished || len(settled.Warnings) != 0 {
		t.Fatalf("settled before lag = %#v", settled)
	}

	finished := state.FinishAfterLag()
	if !finished.Finished || !reflect.DeepEqual(finished.Failed, []string{"alpha"}) {
		t.Fatalf("finished after lag = %#v", finished)
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

func TestMcpStartupReportedOnlySettlesOnObservedStartsMatchRust(t *testing.T) {
	// Rust #60280: a shared app server may run servers the local configuration
	// does not know about, so only the observed startups belong to the round.
	state := NewMcpStartupRoundState([]string{"configured"})
	state.TrackReportedServers()

	started := state.Update("alpha", McpStartupStatus{Kind: McpStartupStarting}, true)
	if !started.Active || started.Settled || started.Finished {
		t.Fatalf("alpha start = %#v", started)
	}
	state.Update("beta", McpStartupStatus{Kind: McpStartupStarting}, true)
	if alphaReady := state.Update("alpha", McpStartupStatus{Kind: McpStartupReady}, false); alphaReady.Settled {
		t.Fatalf("alpha ready must keep waiting for beta: %#v", alphaReady)
	}
	if lateStart := state.Update("late", McpStartupStatus{Kind: McpStartupStarting}, false); lateStart.Settled {
		t.Fatalf("a newly observed start stays in the round: %#v", lateStart)
	}
	state.Update("beta", McpStartupStatus{Kind: McpStartupReady}, false)
	finished := state.Update("late", McpStartupStatus{Kind: McpStartupReady}, true)
	if !finished.Finished || len(finished.Failed) != 0 || len(finished.Cancelled) != 0 {
		t.Fatalf("reported-only round settled on observed starts = %#v", finished)
	}
}

func TestMcpStartupReportedOnlyIdleTerminalDiagnosticsMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState(nil)
	state.TrackReportedServers()
	failure := McpStartupStatus{Kind: McpStartupFailed, Error: "Session expired", FailureReason: "reauthenticationRequired"}

	failed := state.Update("deferred", failure, true)
	if !failed.StandaloneDiagnostic || failed.Active || failed.Finished || len(state.Status) != 0 {
		t.Fatalf("idle failure = %#v state=%#v", failed, state)
	}
	if !reflect.DeepEqual(failed.Failed, []string{"deferred"}) {
		t.Fatalf("idle failure servers = %#v", failed.Failed)
	}
	if got := strings.Join(failed.Warnings, "\n"); !strings.Contains(got, "Session expired") || !strings.Contains(got, "MCP startup incomplete (failed: deferred)") {
		t.Fatalf("idle failure warnings = %#v", failed.Warnings)
	}

	repeat := state.Update("deferred", failure, true)
	if repeat.StandaloneDiagnostic || repeat.Failed != nil || len(repeat.Warnings) != 0 || len(state.Status) != 0 {
		t.Fatalf("repeated idle failure = %#v state=%#v", repeat, state)
	}

	cancelled := state.Update("other", McpStartupStatus{Kind: McpStartupCancelled}, true)
	if !cancelled.StandaloneDiagnostic || !reflect.DeepEqual(cancelled.Cancelled, []string{"other"}) {
		t.Fatalf("idle cancellation = %#v", cancelled)
	}
	if got := strings.Join(cancelled.Warnings, "\n"); !strings.Contains(got, "MCP startup interrupted. The following servers were not initialized: other") {
		t.Fatalf("idle cancellation warnings = %#v", cancelled.Warnings)
	}

	ready := state.Update("eager", McpStartupStatus{Kind: McpStartupReady}, true)
	if ready.StandaloneDiagnostic || ready.Active || len(ready.Warnings) != 0 || len(state.Status) != 0 {
		t.Fatalf("idle ready = %#v state=%#v", ready, state)
	}
}

func TestMcpStartupReportedOnlyReannouncesAfterObservedRetryMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState(nil)
	state.TrackReportedServers()
	failure := McpStartupStatus{Kind: McpStartupFailed, Error: "Session expired"}
	if first := state.Update("deferred", failure, true); !first.StandaloneDiagnostic {
		t.Fatalf("first idle failure = %#v", first)
	}

	started := state.Update("deferred", McpStartupStatus{Kind: McpStartupStarting}, true)
	if !started.Active || started.Settled {
		t.Fatalf("observed retry start = %#v", started)
	}
	retry := state.Update("deferred", failure, true)
	if retry.StandaloneDiagnostic || !retry.Finished || !reflect.DeepEqual(retry.Failed, []string{"deferred"}) {
		t.Fatalf("retry failure = %#v", retry)
	}
	if got := strings.Join(retry.Warnings, "\n"); !strings.Contains(got, "Session expired") || !strings.Contains(got, "MCP startup incomplete (failed: deferred)") {
		t.Fatalf("retry warnings = %#v", retry.Warnings)
	}
}

func TestMcpStartupReportedOnlyRechecksIdenticalFailureAfterLagMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState(nil)
	state.TrackReportedServers()
	failure := McpStartupStatus{Kind: McpStartupFailed, Error: "handshake failed"}
	first := state.Update("alpha", failure, true)
	if len(first.Warnings) == 0 {
		t.Fatalf("first failure = %#v", first)
	}

	if lag := state.FinishAfterLag(); lag.Finished || len(state.Status) != 0 {
		t.Fatalf("lag without an active round = %#v state=%#v", lag, state)
	}
	again := state.Update("alpha", failure, true)
	if !again.StandaloneDiagnostic || !reflect.DeepEqual(again.Warnings, first.Warnings) {
		t.Fatalf("identical failure after lag = %#v, want warnings %#v", again, first.Warnings)
	}
	if len(state.Status) != 0 {
		t.Fatalf("diagnostics reopened startup: %#v", state)
	}
}

func TestMcpStartupReportedOnlyRoundSummaryIncludesAllOutcomesMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState(nil)
	state.TrackReportedServers()
	failure := McpStartupStatus{Kind: McpStartupFailed, Error: "Session expired"}

	state.Update("deferred", failure, true)
	state.Update("deferred", failure, true)
	state.Update("other", McpStartupStatus{Kind: McpStartupCancelled}, true)
	state.Update("deferred", McpStartupStatus{Kind: McpStartupStarting}, true)
	firstRound := state.Update("deferred", failure, true)
	if !firstRound.Finished || !reflect.DeepEqual(firstRound.Failed, []string{"deferred"}) {
		t.Fatalf("first round = %#v", firstRound)
	}

	// The next reload only delivered eager's start; both terminal outcomes and
	// the cancellation still count in the round summary.
	state.Update("eager", McpStartupStatus{Kind: McpStartupStarting}, true)
	state.Update("deferred", failure, false)
	state.Update("other", McpStartupStatus{Kind: McpStartupCancelled}, false)
	last := state.Update("eager", failure, true)
	if !last.Finished {
		t.Fatalf("second round = %#v", last)
	}
	if !reflect.DeepEqual(last.Failed, []string{"deferred", "eager"}) || !reflect.DeepEqual(last.Cancelled, []string{"other"}) {
		t.Fatalf("second round outcomes failed=%#v cancelled=%#v", last.Failed, last.Cancelled)
	}
	if got := strings.Join(last.Warnings, "\n"); !strings.Contains(got, "MCP startup interrupted. The following servers were not initialized: other") || !strings.Contains(got, "MCP startup incomplete (failed: deferred, eager)") {
		t.Fatalf("second round warnings = %#v", last.Warnings)
	}
}
