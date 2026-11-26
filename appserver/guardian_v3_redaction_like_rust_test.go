package appserver

import (
	"context"
	"strings"
	"testing"

	"codex_go/agentboard"
)

// Mirrors Rust #61795: a malformed routing answer must not echo the model's
// response content back in the error.
func TestGuardianRoutingErrorsDoNotEchoModelContentLikeRust(t *testing.T) {
	const secret = "sk-live-must-not-leak-61795"
	if _, err := ParseGuardianRoutingDecision(`{"routes": [` + secret + `]}`); err == nil {
		t.Fatal("malformed routing answer decoded without error")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatalf("parse error echoed the model answer: %v", err)
	}

	decider, err := NewGuardianRoutingDecider(func(context.Context, string, string) (string, error) {
		return "not json at all " + secret, nil
	}, GuardianV3DeciderOptions{})
	if err != nil {
		t.Fatalf("NewGuardianRoutingDecider() error = %v", err)
	}
	_, err = decider(context.Background(), agentboard.GuardianRoutingBatch{
		Catalog: map[string]*string{},
		Sources: []agentboard.GuardianRoutingSource{{Text: "original"}},
	})
	if err == nil {
		t.Fatal("decider accepted a non-JSON answer")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("decider error echoed the model answer: %v", err)
	}
}
