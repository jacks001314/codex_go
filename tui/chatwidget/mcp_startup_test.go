package chatwidget

import (
	"reflect"
	"strings"
	"testing"
)

// Rust #60622: a startup round settles once every start the client observed has
// a terminal outcome. Locally configured servers the app server never reports
// are not part of the round.
func TestMcpStartupObservedRoundSettlesLikeRust(t *testing.T) {
	state := NewMcpStartupRoundState()
	started := state.Update("alpha", McpStartupStatus{Kind: McpStartupStarting})
	if !started.Active || started.Header != MCPStartupSingleHeaderPrefix+" alpha" || started.Finished {
		t.Fatalf("alpha start = %#v", started)
	}
	if beta := state.Update("beta", McpStartupStatus{Kind: McpStartupStarting}); beta.Header == "" {
		t.Fatalf("beta start = %#v", beta)
	}
	if ready := state.Update("alpha", McpStartupStatus{Kind: McpStartupReady}); ready.Finished {
		t.Fatalf("alpha ready must keep waiting for beta: %#v", ready)
	}
	if late := state.Update("late", McpStartupStatus{Kind: McpStartupStarting}); late.Finished {
		t.Fatalf("a newly observed start stays in the round: %#v", late)
	}
	state.Update("beta", McpStartupStatus{Kind: McpStartupReady})
	finished := state.Update("late", McpStartupStatus{Kind: McpStartupReady})
	if !finished.Finished || len(finished.Failed) != 0 || len(finished.Cancelled) != 0 {
		t.Fatalf("round settled on observed starts = %#v", finished)
	}
}

// Rust #60622: an unreported locally configured server must not keep startup
// busy after the observed servers finish.
func TestMcpStartupUnreportedConfiguredServerDoesNotStayBusyLikeRust(t *testing.T) {
	state := NewMcpStartupRoundState()
	state.Update("first", McpStartupStatus{Kind: McpStartupStarting})
	running := state.Update("first", McpStartupStatus{Kind: McpStartupReady})
	if !running.Finished || len(state.Status) != 0 {
		t.Fatalf("single observed server did not settle: %#v state=%#v", running, state)
	}
}

func TestMcpStartupIdleTerminalDiagnosticsMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState()
	failure := McpStartupStatus{Kind: McpStartupFailed, Error: "Session expired", FailureReason: "reauthenticationRequired"}

	failed := state.Update("deferred", failure)
	if !failed.StandaloneDiagnostic || failed.Active || failed.Finished || len(state.Status) != 0 {
		t.Fatalf("idle failure = %#v state=%#v", failed, state)
	}
	if !reflect.DeepEqual(failed.Failed, []string{"deferred"}) {
		t.Fatalf("idle failure servers = %#v", failed.Failed)
	}
	if got := strings.Join(failed.Warnings, "\n"); !strings.Contains(got, "Session expired") || !strings.Contains(got, "MCP startup incomplete (failed: deferred)") {
		t.Fatalf("idle failure warnings = %#v", failed.Warnings)
	}

	repeat := state.Update("deferred", failure)
	if repeat.StandaloneDiagnostic || repeat.Failed != nil || len(repeat.Warnings) != 0 || len(state.Status) != 0 {
		t.Fatalf("repeated idle failure = %#v state=%#v", repeat, state)
	}

	cancelled := state.Update("other", McpStartupStatus{Kind: McpStartupCancelled})
	if !cancelled.StandaloneDiagnostic || !reflect.DeepEqual(cancelled.Cancelled, []string{"other"}) {
		t.Fatalf("idle cancellation = %#v", cancelled)
	}
	if got := strings.Join(cancelled.Warnings, "\n"); !strings.Contains(got, "MCP startup interrupted. The following servers were not initialized: other") {
		t.Fatalf("idle cancellation warnings = %#v", cancelled.Warnings)
	}

	ready := state.Update("eager", McpStartupStatus{Kind: McpStartupReady})
	if ready.StandaloneDiagnostic || ready.Active || len(ready.Warnings) != 0 || len(state.Status) != 0 {
		t.Fatalf("idle ready = %#v state=%#v", ready, state)
	}
}

func TestMcpStartupReannouncesAfterObservedRetryMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState()
	failure := McpStartupStatus{Kind: McpStartupFailed, Error: "Session expired"}
	if first := state.Update("deferred", failure); !first.StandaloneDiagnostic {
		t.Fatalf("first idle failure = %#v", first)
	}

	started := state.Update("deferred", McpStartupStatus{Kind: McpStartupStarting})
	if !started.Active || started.Finished {
		t.Fatalf("observed retry start = %#v", started)
	}
	retry := state.Update("deferred", failure)
	if retry.StandaloneDiagnostic || !retry.Finished || !reflect.DeepEqual(retry.Failed, []string{"deferred"}) {
		t.Fatalf("retry failure = %#v", retry)
	}
	if got := strings.Join(retry.Warnings, "\n"); !strings.Contains(got, "Session expired") || !strings.Contains(got, "MCP startup incomplete (failed: deferred)") {
		t.Fatalf("retry warnings = %#v", retry.Warnings)
	}
}

func TestMcpStartupRechecksIdenticalFailureAfterLagMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState()
	failure := McpStartupStatus{Kind: McpStartupFailed, Error: "handshake failed"}
	first := state.Update("alpha", failure)
	if len(first.Warnings) == 0 {
		t.Fatalf("first failure = %#v", first)
	}

	if lag := state.FinishAfterLag(); lag.Finished || len(state.Status) != 0 {
		t.Fatalf("lag without an active round = %#v state=%#v", lag, state)
	}
	again := state.Update("alpha", failure)
	if !again.StandaloneDiagnostic || !reflect.DeepEqual(again.Warnings, first.Warnings) {
		t.Fatalf("identical failure after lag = %#v, want warnings %#v", again, first.Warnings)
	}
	if len(state.Status) != 0 {
		t.Fatalf("diagnostics reopened startup: %#v", state)
	}
}

func TestMcpStartupRoundSummaryIncludesAllOutcomesMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState()
	failure := McpStartupStatus{Kind: McpStartupFailed, Error: "Session expired"}

	state.Update("deferred", failure)
	state.Update("deferred", failure)
	state.Update("other", McpStartupStatus{Kind: McpStartupCancelled})
	state.Update("deferred", McpStartupStatus{Kind: McpStartupStarting})
	firstRound := state.Update("deferred", failure)
	if !firstRound.Finished || !reflect.DeepEqual(firstRound.Failed, []string{"deferred"}) {
		t.Fatalf("first round = %#v", firstRound)
	}

	// The next reload starts eager; deferred and other terminal outcomes are
	// delivered again before eager finishes, and all of them count in the round.
	state.Update("eager", McpStartupStatus{Kind: McpStartupStarting})
	state.Update("deferred", failure)
	state.Update("other", McpStartupStatus{Kind: McpStartupCancelled})
	last := state.Update("eager", failure)
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

func TestMcpStartupPreservesServerAndErrorTextMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState()
	started := state.Update(" alpha ", McpStartupStatus{Kind: McpStartupStarting})
	if started.Header != MCPStartupSingleHeaderPrefix+"  alpha " {
		t.Fatalf("header should preserve server text, got %q", started.Header)
	}

	finished := state.Update(" alpha ", McpStartupStatus{Kind: McpStartupFailed, Error: "  boom  "})
	if len(finished.Warnings) < 1 || !reflect.DeepEqual(finished.Warnings[:1], []string{"  boom  "}) {
		t.Fatalf("warnings should preserve error text: %#v", finished.Warnings)
	}
}

func TestMcpStartupReactivatesOnNewStartAfterFinishMatchRust(t *testing.T) {
	state := NewMcpStartupRoundState()
	state.Update("alpha", McpStartupStatus{Kind: McpStartupStarting})
	if first := state.Update("alpha", McpStartupStatus{Kind: McpStartupReady}); !first.Finished {
		t.Fatalf("first round = %#v", first)
	}
	started := state.Update("beta", McpStartupStatus{Kind: McpStartupStarting})
	if !started.Active || started.Header != MCPStartupSingleHeaderPrefix+" beta" {
		t.Fatalf("new round = %#v", started)
	}
	finished := state.Update("beta", McpStartupStatus{Kind: McpStartupFailed, Error: "beta failed"})
	if !finished.Finished || !reflect.DeepEqual(finished.Failed, []string{"beta"}) {
		t.Fatalf("second round = %#v", finished)
	}
}
