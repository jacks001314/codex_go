package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

type delayedHostTimingSession struct {
	delay    time.Duration
	response CodeModeRemoteResponse
}

func (s *delayedHostTimingSession) Execute(context.Context, CodeModeRemoteExecuteRequest, *YieldSignal) (CodeModeRemoteResponse, error) {
	time.Sleep(s.delay)
	return s.response, nil
}

func (s *delayedHostTimingSession) Wait(context.Context, string, uint64, *int, *YieldSignal) (CodeModeRemoteResponse, error) {
	time.Sleep(s.delay)
	return s.response, nil
}

func (s *delayedHostTimingSession) Terminate(context.Context, string, *int) (CodeModeRemoteResponse, error) {
	time.Sleep(s.delay)
	return s.response, nil
}

func (*delayedHostTimingSession) Close() error { return nil }

func TestCodeModeModelWallTimeUsesHostDurationLikeRust(t *testing.T) {
	const hostDuration = 100 * time.Millisecond
	session := &delayedHostTimingSession{
		delay: 300 * time.Millisecond,
		response: CodeModeRemoteResponse{
			CellID:         "timed-cell",
			State:          "completed",
			ContentItems:   []map[string]any{{"type": "input_text", "text": "TIMED"}},
			HostDurationNS: uint64(hostDuration),
		},
	}
	runtime := NewCodeModeRuntime(&recordingCodeModeRemoteProvider{session: session}, false)
	var completed CodeModeCallObservation
	runtime.SetCallObserver(func(observation CodeModeCallObservation) {
		if observation.Kind == CodeModeCallCompleted {
			completed = observation
		}
	})
	exec, _ := runtime.Executors(NewRegistry())
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "timed-call",
		Payload: Payload{Kind: PayloadCustom, Input: `text("TIMED")`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.HasPrefix(output.Body, "Script completed\nWall time 0.1 seconds\nOutput:\n") {
		t.Fatalf("model-visible wall time used client elapsed: %q", output.Body)
	}
	if completed.CodeModeHostDurationNS != uint64(hostDuration) {
		t.Fatalf("completed observation host duration = %d, want %d", completed.CodeModeHostDurationNS, hostDuration)
	}
}
