package exec

import (
	"bytes"
	"strings"
	"testing"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/model"
)

// TestRunAppliesPromptCacheKeyPolicyLikeRust covers the three policies from
// Rust #55587 on the Go host surface (exec.Request).
func TestRunAppliesPromptCacheKeyPolicyLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		policy model.PromptCacheKey
		want   func(threadID string) string
	}{
		{
			name:   "session derived",
			policy: model.SessionDerivedPromptCacheKey(),
			want:   func(threadID string) string { return threadID },
		},
		{
			name:   "custom",
			policy: model.CustomPromptCacheKey("caller-key"),
			want:   func(string) string { return "caller-key" },
		},
		{
			name:   "omit",
			policy: model.OmitPromptCacheKey(),
			want:   func(string) string { return "" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			if err := auth.NewStore(home).Save(auth.FromAPIKey("sk-test")); err != nil {
				t.Fatalf("Save auth returned error: %v", err)
			}
			agent := &recordingAgent{message: "ok"}
			runner := NewRunner(home)
			runner.Agent = agent
			var stdout, stderr bytes.Buffer
			result, err := runner.Run(Request{
				Exec:           cli.ExecOptions{Prompt: "hello"},
				PromptCacheKey: test.policy,
			}, strings.NewReader(""), &stdout, &stderr)
			if err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if agent.request == nil {
				t.Fatal("agent received no request")
			}
			if got, want := agent.request.PromptCacheKey, test.want(result.ThreadID); got != want {
				t.Fatalf("PromptCacheKey = %q, want %q", got, want)
			}
		})
	}
}

// TestSubagentRequestInheritsPromptCacheKeyPolicyLikeRust verifies that child
// agent runs reuse the root run's policy, matching Rust's inherited_child_settings.
func TestSubagentRequestInheritsPromptCacheKeyPolicyLikeRust(t *testing.T) {
	for _, policy := range []model.PromptCacheKey{
		model.SessionDerivedPromptCacheKey(),
		model.CustomPromptCacheKey("root-key"),
		model.OmitPromptCacheKey(),
	} {
		parent := Request{PromptCacheKey: policy}
		child := cloneExecAgentRequest(&parent)
		if child.PromptCacheKey != policy {
			t.Fatalf("child policy = %#v, want %#v", child.PromptCacheKey, policy)
		}
	}
}
