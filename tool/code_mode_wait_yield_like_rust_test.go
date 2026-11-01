package tool

import (
	"context"
	"testing"
	"time"
)

// Mirrors Rust's ExecWaitArgs.yield_time_ms: the 10 000 ms wait is a serde
// default for an absent field, so an explicit 0 polls the cell without
// blocking instead of waiting the default.

type yieldRecordingSession struct {
	waitYield    uint64
	waitRecorded bool
	response     CodeModeRemoteResponse
}

func (s *yieldRecordingSession) Execute(context.Context, CodeModeRemoteExecuteRequest, *YieldSignal) (CodeModeRemoteResponse, error) {
	return CodeModeRemoteResponse{CellID: "yield-cell", State: "yielded"}, nil
}

func (s *yieldRecordingSession) Wait(_ context.Context, _ string, yieldMS uint64, _ *int, _ *YieldSignal) (CodeModeRemoteResponse, error) {
	s.waitYield = yieldMS
	s.waitRecorded = true
	return s.response, nil
}

func (s *yieldRecordingSession) Terminate(context.Context, string, *int) (CodeModeRemoteResponse, error) {
	return s.response, nil
}

func (s *yieldRecordingSession) Close() error { return nil }

type yieldRecordingProvider struct{ session *yieldRecordingSession }

func (p *yieldRecordingProvider) NewSession(CodeModeRemoteDelegate) CodeModeRemoteSession {
	return p.session
}

func runRemoteWaitWithYield(t *testing.T, arguments string) (*yieldRecordingSession, error) {
	t.Helper()
	session := &yieldRecordingSession{response: CodeModeRemoteResponse{CellID: "yield-cell", State: "completed"}}
	runtime := NewCodeModeRuntime(&yieldRecordingProvider{session: session}, false)
	_, wait := runtime.Executors(NewRegistry())
	_, err := wait.Execute(context.Background(), &Invocation{
		CallID:  "wait-yield",
		Payload: Payload{Kind: PayloadFunction, Arguments: arguments},
	})
	return session, err
}

func TestRemoteWaitForwardsAnExplicitZeroYieldLikeRust(t *testing.T) {
	session, err := runRemoteWaitWithYield(t, `{"cell_id":"yield-cell","yield_time_ms":0}`)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if !session.waitRecorded {
		t.Fatal("wait did not reach the remote session")
	}
	if session.waitYield != 0 {
		t.Fatalf("forwarded yield = %d, want an explicit 0", session.waitYield)
	}
}

func TestRemoteWaitDefaultsAnAbsentYieldLikeRust(t *testing.T) {
	session, err := runRemoteWaitWithYield(t, `{"cell_id":"yield-cell"}`)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if session.waitYield != 10000 {
		t.Fatalf("forwarded yield = %d, want the 10 000 ms default", session.waitYield)
	}
}

func TestRemoteWaitRejectsANegativeYieldLikeRust(t *testing.T) {
	session, err := runRemoteWaitWithYield(t, `{"cell_id":"yield-cell","yield_time_ms":-1}`)
	if err == nil {
		t.Fatalf("negative yield was accepted (forwarded %d)", session.waitYield)
	}
	if session.waitRecorded {
		t.Fatalf("negative yield reached the remote session: %d", session.waitYield)
	}
}

// The in-process wait uses the same resolution: an explicit zero observes the
// running cell immediately instead of blocking for the default 10 seconds.
func TestLocalWaitPollsImmediatelyForAnExplicitZeroYieldLikeRust(t *testing.T) {
	exec, wait := NewCodeModeExecutors(NewRegistry())
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "cell-zero-yield",
		Payload: Payload{Kind: PayloadCustom, Input: "// @exec: {\"yield_time_ms\": 0}\ntext('abcdefgh'); await new Promise(resolve => setTimeout(resolve, 400));"},
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	cellID, _ := output.Data["cell_id"].(string)
	if cellID == "" {
		t.Fatalf("exec did not yield a cell: %#v", output.Data)
	}
	started := time.Now()
	outcome, err := wait.Execute(context.Background(), &Invocation{
		CallID:  "cell-zero-yield-wait",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","yield_time_ms":0}`},
	})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("zero-yield wait blocked for %v", elapsed)
	}
	if running, _ := outcome.Data["running"].(bool); !running {
		t.Fatalf("zero-yield wait did not report the running cell: %#v", outcome.Data)
	}
	// The cell keeps running on its own and completes on a later wait.
	completed, err := wait.Execute(context.Background(), &Invocation{
		CallID:  "cell-zero-yield-wait-again",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","yield_time_ms":2000}`},
	})
	if err != nil {
		t.Fatalf("wait after settle: %v", err)
	}
	if completed.Data["running"] == true {
		t.Fatalf("cell still running after its timer fired: %#v", completed.Data)
	}
}
