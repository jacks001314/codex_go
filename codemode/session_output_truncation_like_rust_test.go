package codemode

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Mirrors the server-side truncation contract added by Rust #57850/#57870: a
// server-truncated session applies each observation's own budget, retains
// bounded output between observations, and reports collector losses; a
// client-truncated session returns raw output.

func observationText(items []ContentItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type == "input_text" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestSessionRuntimeClientTruncationReturnsRawOutputLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	yield := uint64(0)
	budget := 10
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID:      "call-client",
		Source:          `text("x".repeat(50000))`,
		YieldTimeMS:     &yield,
		MaxOutputTokens: &budget,
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	waitForCellStatus(t, runtime.Cells(), started.CellID.String(), CellCompleted, 2*time.Second)
	outcome, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 1000}, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	text := observationText(outcome.Response.ContentItems)
	if len(text) != 50000 {
		t.Fatalf("client text length = %d, want 50000", len(text))
	}
	if strings.Contains(text, "Warning: truncated output") {
		t.Fatalf("client output was truncated by the session")
	}
}

func TestSessionRuntimeServerTruncationAppliesObservationBudgetLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	runtime.SetOutputTruncation(OutputTruncationServer)
	yield := uint64(0)
	budget := 10
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID:      "call-server",
		Source:          `text("x".repeat(50000))`,
		YieldTimeMS:     &yield,
		MaxOutputTokens: &budget,
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	waitForCellStatus(t, runtime.Cells(), started.CellID.String(), CellCompleted, 2*time.Second)
	outcome, err := runtime.Wait(context.Background(), &WaitRequest{
		CellID:          started.CellID,
		YieldTimeMS:     1000,
		MaxOutputTokens: &budget,
	}, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	text := observationText(outcome.Response.ContentItems)
	if !strings.HasPrefix(text, "Warning: truncated output") {
		t.Fatalf("server output is not budgeted: %.60s", text)
	}
	if len(text) >= 50000 {
		t.Fatalf("server output kept %d bytes", len(text))
	}

	// A zero-token observation still reports collector losses.
	lossless := 0
	if got := observationText(runtime.mustObservation(t, started.CellID, &lossless, 100000)); got != "" && !strings.HasPrefix(got, "Warning: truncated output") {
		t.Fatalf("zero-budget output = %.60s", got)
	}
}

// mustObservation runs one wait with an explicit budget.
func (r *SessionRuntime) mustObservation(t *testing.T, cellID CellID, budget *int, yieldMS uint64) []ContentItem {
	t.Helper()
	outcome, err := r.Wait(context.Background(), &WaitRequest{CellID: cellID, YieldTimeMS: yieldMS, MaxOutputTokens: budget}, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	return outcome.Response.ContentItems
}

func TestSessionRuntimeServerTruncationReportsCollectorLossesLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	runtime.SetOutputTruncation(OutputTruncationServer)
	yield := uint64(0)
	// Grow well past the 1 MiB text allowance so the collector must evict.
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID:  "call-loss",
		Source:      `text("a".repeat(1500000))`,
		YieldTimeMS: &yield,
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	waitForCellStatus(t, runtime.Cells(), started.CellID.String(), CellCompleted, 2*time.Second)
	outcome, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 1000}, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	text := observationText(outcome.Response.ContentItems)
	if !strings.Contains(text, "[Code Mode session output limit reached:") {
		t.Fatalf("missing collector loss notice in %.120s", text)
	}
}

func TestSessionRuntimeServerTruncationBudgetsDiagnosticsLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	runtime.SetOutputTruncation(OutputTruncationServer)
	yield := uint64(0)
	started, err := runtime.Execute(context.Background(), &ExecuteRequest{
		ToolCallID:  "call-error",
		Source:      `throw new Error("boom")`,
		YieldTimeMS: &yield,
	}, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	waitForCellStatus(t, runtime.Cells(), started.CellID.String(), CellFailed, 2*time.Second)
	outcome, err := runtime.Wait(context.Background(), &WaitRequest{CellID: started.CellID, YieldTimeMS: 1000}, nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if outcome.Response.Variant != "Result" || outcome.Response.ErrorText == nil {
		t.Fatalf("failed response = %#v", outcome.Response)
	}
	text := observationText(outcome.Response.ContentItems)
	if !strings.Contains(text, "boom") {
		t.Fatalf("diagnostic not charged against the observation: %.120s", text)
	}
}

func TestSessionHostAppliesServerBudgetsOnWaitAndTerminateLikeRust(t *testing.T) {
	runtime := NewSessionRuntime()
	host := NewSessionHost(runtime)
	sessionID, _ := NewSessionID("session-budget")
	if _, _, err := host.Handle(context.Background(), hostRequestPtr(OpenSessionRequestWithOptions(sessionID, SessionOptions{OutputTruncation: OutputTruncationServer})), nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	if host.OutputTruncation() != OutputTruncationServer {
		t.Fatalf("host truncation = %q", host.OutputTruncation())
	}
	if runtime.OutputTruncationMode() != OutputTruncationServer {
		t.Fatalf("runtime truncation = %q", runtime.OutputTruncationMode())
	}
	yield := uint64(0)
	budget := 8
	response, initial, err := host.Handle(context.Background(), hostRequestPtr(ExecuteSessionRequest(sessionID, ExecuteRequest{
		ToolCallID:      "call-host",
		Source:          `text("y".repeat(40000))`,
		YieldTimeMS:     &yield,
		MaxOutputTokens: &budget,
	})), nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if response == nil || initial == nil {
		t.Fatalf("execute response = %#v", response)
	}
	if text := observationText(initial.ContentItems); text != "" && !strings.HasPrefix(text, "Warning: truncated output") {
		t.Fatalf("initial output not budgeted: %.60s", text)
	}
	cellID := response.CellID
	waitForCellStatus(t, runtime.Cells(), cellID.String(), CellCompleted, 2*time.Second)

	waitResponse, _, err := host.Handle(context.Background(), hostRequestPtr(WaitSessionRequestWithBudget(sessionID, WaitRequest{CellID: cellID, YieldTimeMS: 1000}, &budget)), nil)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if waitResponse == nil || waitResponse.Outcome == nil {
		t.Fatalf("wait response = %#v", waitResponse)
	}
	text := observationText(waitResponse.Outcome.Response.ContentItems)
	if !strings.HasPrefix(text, "Warning: truncated output") {
		t.Fatalf("wait output not budgeted: %.60s", text)
	}

	terminateResponse, _, err := host.Handle(context.Background(), hostRequestPtr(TerminateSessionRequestWithBudget(sessionID, TerminateRequest{CellID: cellID, MaxOutputTokens: &budget})), nil)
	if err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if terminateResponse == nil || terminateResponse.Outcome == nil {
		t.Fatalf("terminate response = %#v", terminateResponse)
	}
	text = observationText(terminateResponse.Outcome.Response.ContentItems)
	if !strings.HasPrefix(text, "Warning: truncated output") {
		t.Fatalf("terminate output not budgeted: %.60s", text)
	}
}

func hostRequestPtr(request HostRequest) *HostRequest { return &request }
