package turn

import (
	"context"
	"errors"
	"testing"
)

// Rust #58430: a steer whose input the host rejects (uploaded audio) fails the
// running turn before it reaches inference, instead of the steer being refused.
func TestAgentLoopFailsTurnWhenSteerInputIsRejectedLikeRust(t *testing.T) {
	mailbox := NewSteerMailbox()
	if err := mailbox.Enqueue(&SteerEnqueueParams{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		InputItems: []any{map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{{
				"type":    "input_audio",
				"file_id": "file_audio",
			}},
		}},
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	agent := &fakeLoopAgent{}
	loop := NewAgentLoop(&AgentLoopOptions{Agent: agent, SteerMailbox: mailbox, MaxTurns: 1})
	rejected := errors.New("Uploaded audio file inputs are not enabled in this build.")
	validated := 0
	_, err := loop.Run(context.Background(), &AgentLoopRequest{
		Prompt:       "hello",
		ThreadID:     "thread-1",
		TurnID:       "turn-1",
		SteerMailbox: mailbox,
		ValidateSteerInput: func(items []any) error {
			validated++
			if len(items) != 1 {
				t.Fatalf("validated items = %#v, want the delivered steer", items)
			}
			return rejected
		},
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("Run() error = %v, want the rejection", err)
	}
	if validated != 1 {
		t.Fatalf("ValidateSteerInput calls = %d, want 1", validated)
	}
	if len(agent.requests) != 0 {
		t.Fatalf("the rejected steer reached inference: %#v", agent.requests)
	}
}
