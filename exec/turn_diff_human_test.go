package exec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/protocol"
	"codex_go/tool"
	"codex_go/turn"
)

// TestExecHumanRendererRendersTurnDiffsLikeRust ports the vectors of Rust's
// `turn_diff_notifications_render_only_changes`
// (codex-rs/exec/src/event_processor_with_human_output_tests.rs): repeated
// updates stay quiet, changed and restored diffs still print, blank updates are
// remembered so a restored diff renders again, and a new turn resets the memory.
func TestExecHumanRendererRendersTurnDiffsLikeRust(t *testing.T) {
	const (
		firstDiff   = "@@ -1 +1 @@\n-value=1\n+value=2"
		changedDiff = "@@ -1 +1 @@\n-value=1\n+value=3"
	)
	var stderr bytes.Buffer
	renderer := newExecHumanRenderer(&stderr, "never")
	turns := []struct {
		turnID string
		diffs  []string
	}{
		{"turn-1", []string{"", " \n", firstDiff, firstDiff, changedDiff, changedDiff, firstDiff, "", firstDiff, " \n", firstDiff}},
		{"turn-2", []string{firstDiff, firstDiff}},
	}
	for _, turn := range turns {
		renderer.HandleEvent(protocol.TurnStarted())
		fmt.Fprintf(&stderr, "%s\n", turn.turnID)
		for _, diff := range turn.diffs {
			renderer.HandleTurnDiff(diff)
		}
	}
	want := fmt.Sprintf("turn-1\n%s\n%s\n%s\n%s\n%s\nturn-2\n%s\n", firstDiff, changedDiff, firstDiff, firstDiff, firstDiff, firstDiff)
	if got := stderr.String(); got != want {
		t.Fatalf("turn diff rendering = %q, want %q", got, want)
	}
}

// TestExecTurnDiffRendersOnlyChangesLikeRust mirrors Rust's
// exec/tests/suite/apply_patch.rs::human_output_prints_unchanged_turn_diff_once:
// the accumulated turn diff renders once per change rather than once per
// notification, and a later change renders the new accumulated diff again.
func TestExecTurnDiffRendersOnlyChangesLikeRust(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("value=1\n"), 0o600); err != nil {
		t.Fatalf("write probe.txt: %v", err)
	}
	router, err := turn.BuildToolRouter(turn.DefaultToolRegistryOptions(dir))
	if err != nil {
		t.Fatalf("BuildToolRouter: %v", err)
	}
	executions := []turn.ToolExecutionResult{
		execApplyPatchExecution(t, router, "patch-1",
			"*** Begin Patch\n*** Update File: probe.txt\n@@\n-value=1\n+value=2\n*** End Patch"),
		// Rewriting the file with identical content leaves the accumulated turn
		// diff unchanged, so this notification repeats the previous diff.
		execApplyPatchExecution(t, router, "patch-2",
			"*** Begin Patch\n*** Update File: probe.txt\n@@\n-value=2\n+value=2\n*** End Patch"),
		// A second file changes the accumulated diff, which renders again.
		execApplyPatchExecution(t, router, "patch-3",
			"*** Begin Patch\n*** Add File: extra.txt\n+added\n*** End Patch"),
	}
	responses := make([]*model.AgentResponse, 0, len(executions))
	for i := range executions {
		callID := executions[i].Invocation.CallID
		responses = append(responses, &model.AgentResponse{
			ResponseID: "resp-" + callID,
			Items: []model.AgentItem{{
				ID:     callID,
				Type:   "custom_tool_call",
				Name:   tool.DefaultApplyPatchToolName,
				CallID: callID,
			}},
		})
	}
	result := &turn.AgentLoopResult{Responses: responses, ToolExecutions: executions}
	result.Response = responses[len(responses)-1]

	var stderr bytes.Buffer
	sink := newExecEventSink(nil, false)
	sink.human = newExecHumanRenderer(&stderr, "never")
	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	rendered := stderr.String()
	if got := strings.Count(rendered, "patch: completed"); got != 3 {
		t.Fatalf("patch completions = %d, want 3; stderr=%q", got, rendered)
	}
	if got := strings.Count(rendered, "diff --git a/probe.txt b/probe.txt"); got != 2 {
		t.Fatalf("probe.txt diff renders = %d, want 2 (an unchanged diff prints once); stderr=%q", got, rendered)
	}
	if got := strings.Count(rendered, "-value=1"); got != 2 {
		t.Fatalf("removed-line renders = %d, want 2; stderr=%q", got, rendered)
	}
	if got := strings.Count(rendered, "diff --git a/extra.txt b/extra.txt"); got != 1 {
		t.Fatalf("extra.txt diff renders = %d, want 1; stderr=%q", got, rendered)
	}
	if !strings.Contains(rendered, "+added") {
		t.Fatalf("accumulated diff missing the added file: %q", rendered)
	}
}

// TestExecTurnDiffStaysOutOfJSONLikeRust guards the wire format: Rust's exec JSON
// event model has no turn-diff event (codex-rs/exec/src/exec_events.rs), so the
// accumulated diff that the human renderer now prints must never reach --json.
func TestExecTurnDiffStaysOutOfJSONLikeRust(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe.txt"), []byte("value=1\n"), 0o600); err != nil {
		t.Fatalf("write probe.txt: %v", err)
	}
	router, err := turn.BuildToolRouter(turn.DefaultToolRegistryOptions(dir))
	if err != nil {
		t.Fatalf("BuildToolRouter: %v", err)
	}
	execution := execApplyPatchExecution(t, router, "patch-1",
		"*** Begin Patch\n*** Update File: probe.txt\n@@\n-value=1\n+value=2\n*** End Patch")
	result := &turn.AgentLoopResult{
		Responses: []*model.AgentResponse{{ResponseID: "resp-patch-1", Items: []model.AgentItem{{
			ID: "patch-1", Type: "custom_tool_call", Name: tool.DefaultApplyPatchToolName, CallID: "patch-1",
		}}}},
		ToolExecutions: []turn.ToolExecutionResult{execution},
	}
	result.Response = result.Responses[0]
	var stdout bytes.Buffer
	sink := newExecEventSink(&stdout, true)
	if err := emitFinalEventsFromAgentResult(sink, result, false); err != nil {
		t.Fatalf("emitFinalEventsFromAgentResult() error = %v", err)
	}
	encoded := stdout.String()
	if strings.Contains(encoded, "diff --git") || strings.Contains(encoded, "@@ -1 +1 @@") || strings.Contains(encoded, "turn.diff") {
		t.Fatalf("JSON output leaked the turn diff: %s", encoded)
	}
	if !strings.Contains(encoded, `"type":"turn.completed"`) {
		t.Fatalf("JSON output missing turn.completed: %s", encoded)
	}
}

func execApplyPatchExecution(t *testing.T, router *tool.Router, callID string, patch string) turn.ToolExecutionResult {
	t.Helper()
	invocation := &tool.Invocation{
		CallID:   callID,
		ToolName: tool.PlainName(tool.DefaultApplyPatchToolName),
		Payload:  tool.Payload{Kind: tool.PayloadCustom, Input: patch},
	}
	output, err := router.Dispatch(context.Background(), invocation)
	if err != nil {
		t.Fatalf("dispatch %s: %v", callID, err)
	}
	if output == nil || !output.Success {
		t.Fatalf("apply_patch %s output = %#v", callID, output)
	}
	if output.Data["appliedChanges"] == nil {
		t.Fatalf("apply_patch %s published no appliedChanges: %#v", callID, output.Data)
	}
	return turn.ToolExecutionResult{Invocation: invocation, Output: output}
}
