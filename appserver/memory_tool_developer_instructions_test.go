package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/memories"
	"codex_go/session"
	"codex_go/turn"
)

// TestRuntimeRouterTurnStartEmitsMemoryToolDeveloperInstructionsLikeRust is
// the dynamic end-to-end verification for the memory-tool developer policy
// fragment: Rust's MemoriesExtension contributes the read_path.md developer
// section at thread context when the memories feature is enabled and
// memories.use_memories is true (ext/memories/src/extension.rs). Go mirrors
// this in instructionsWithMemoryToolContext; this test proves the fragment
// reaches the model-visible developer instructions of a real turn, token-free.
func TestRuntimeRouterTurnStartEmitsMemoryToolDeveloperInstructionsLikeRust(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "memories"), 0o755); err != nil {
		t.Fatalf("MkdirAll memories: %v", err)
	}
	summary := "v1\n\nproject conventions: use tabs\n"
	if err := os.WriteFile(filepath.Join(codexHome, "memories", memories.MemorySummaryFilename), []byte(summary), 0o600); err != nil {
		t.Fatalf("WriteFile summary: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(codexHome), []byte("[features]\nmemories = true\n[memories]\nuse_memories = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	instructions := runMemoryToolInstructionsTurn(t, codexHome)
	if !strings.Contains(instructions, "## Memory") {
		t.Fatalf("memory developer instructions missing from turn developer instructions:\n%s", instructions)
	}
	if !strings.Contains(instructions, "project conventions: use tabs") {
		t.Fatalf("memory_summary excerpt missing from developer instructions:\n%s", instructions)
	}
	if !strings.Contains(instructions, "========= MEMORY_SUMMARY BEGINS =========") ||
		!strings.Contains(instructions, "========= MEMORY_SUMMARY ENDS =========") {
		t.Fatalf("memory summary delimiters missing from developer instructions:\n%s", instructions)
	}
}

// TestRuntimeRouterTurnStartOmitsMemoryToolDeveloperInstructionsWhenGated
// pins the negative gates: the fragment must stay absent when the feature is
// disabled, when use_memories is false, or when no summary has been persisted
// (Rust build_memory_tool_developer_instructions returns None in all three
// cases).
func TestRuntimeRouterTurnStartOmitsMemoryToolDeveloperInstructionsWhenGated(t *testing.T) {
	cases := []struct {
		name          string
		configTOML    string
		writeSummary  bool
		summary       string
	}{
		{name: "feature-disabled", configTOML: "", writeSummary: true, summary: "v1\nconventions: tabs\n"},
		{name: "use-memories-false", configTOML: "[features]\nmemories = true\n[memories]\nuse_memories = false\n", writeSummary: true, summary: "v1\nconventions: tabs\n"},
		{name: "summary-missing", configTOML: "[features]\nmemories = true\n[memories]\nuse_memories = true\n", writeSummary: false},
		{name: "summary-empty", configTOML: "[features]\nmemories = true\n[memories]\nuse_memories = true\n", writeSummary: true, summary: "   \n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			codexHome := filepath.Join(home, "codex")
			if err := os.MkdirAll(filepath.Join(codexHome, "memories"), 0o755); err != nil {
				t.Fatalf("MkdirAll memories: %v", err)
			}
			if tc.writeSummary {
				if err := os.WriteFile(filepath.Join(codexHome, "memories", memories.MemorySummaryFilename), []byte(tc.summary), 0o600); err != nil {
					t.Fatalf("WriteFile summary: %v", err)
				}
			}
			if tc.configTOML != "" {
				if err := os.WriteFile(config.ConfigPath(codexHome), []byte(tc.configTOML), 0o600); err != nil {
					t.Fatalf("WriteFile config: %v", err)
				}
			}
			instructions := runMemoryToolInstructionsTurn(t, codexHome)
			if strings.Contains(instructions, "## Memory") {
				t.Fatalf("memory developer instructions unexpectedly present when %s:\n%s", tc.name, instructions)
			}
		})
	}
}

// runMemoryToolInstructionsTurn starts a paginated thread and one turn with a
// recording agent under the given codex home, and returns the
// model-visible developer instructions of the turn (AgentRequest.Instructions).
func runMemoryToolInstructionsTurn(t *testing.T, codexHome string) string {
	t.Helper()
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Config:       config.NewConfigService(codexHome),
	})
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
	return request.Instructions
}
