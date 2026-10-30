package appserver

import (
	"strings"
	"testing"

	codexctx "codex_go/context"
	"codex_go/session"
	"codex_go/state"
	"codex_go/tool"
	"codex_go/turn"
	"codex_go/utils"
)

// Mirrors Rust #57176's node_repl_result_token_limit: the larger node_repl
// output cap applies only while node_repl evidence is enabled; otherwise the
// ordinary tool entry cap bounds the entry.
func TestGuardianNodeReplResultTokenLimitFollowsEvidenceModeLikeRust(t *testing.T) {
	if codexctx.GuardianMaxNodeReplToolResultTokens <= state.MaxToolEntryTokens {
		t.Fatalf("enhanced cap %d must exceed the tool entry cap %d", codexctx.GuardianMaxNodeReplToolResultTokens, state.MaxToolEntryTokens)
	}
	if got := guardianNodeReplResultTokenLimitForMode(codexctx.NodeReplEvidenceDisabled); got != state.MaxToolEntryTokens {
		t.Fatalf("disabled evidence mode limit = %d, want %d", got, state.MaxToolEntryTokens)
	}
	for _, mode := range []codexctx.NodeReplReviewEvidenceMode{codexctx.NodeReplEvidenceTextOnly, codexctx.NodeReplEvidenceMultimodal} {
		if got := guardianNodeReplResultTokenLimitForMode(mode); got != codexctx.GuardianMaxNodeReplToolResultTokens {
			t.Fatalf("enabled evidence mode %v limit = %d, want %d", mode, got, codexctx.GuardianMaxNodeReplToolResultTokens)
		}
	}
}

// Mirrors Rust #57176: an oversized node_repl tool output is truncated to the
// selected cap when the transcript is prepared. Enhanced node_repl transcripts
// admit the larger cap, while the default configuration keeps the tool cap.
func TestGuardianReviewTranscriptNodeReplOutputLimitLikeRust(t *testing.T) {
	now := fixedTime()
	oversized := strings.Repeat("r", 3*utils.ApproxBytesForTokens(state.MaxToolEntryTokens))
	router := guardianProfileTestRouter(t, []session.Item{
		{ID: "call", Type: "function_call", Role: "assistant", Name: "js", Namespace: "node_repl", CallID: "call-1", Text: `{"code":"1"}`, CreatedAt: now},
		{ID: "output", Type: "function_call_output", Name: "js", Namespace: "node_repl", CallID: "call-1", Text: oversized, CreatedAt: now},
	})

	defaultSelection := router.guardianReviewTranscriptSelection(guardianProfileTestThreadID)
	defaultOutput := guardianTranscriptOutputText(t, defaultSelection)
	if !strings.Contains(defaultOutput, "<truncated omitted_approx_tokens=") {
		t.Fatalf("default selection must cap the node_repl output at the tool entry cap: %q", defaultOutput[:min(len(defaultOutput), 96)])
	}

	if err := router.threads.RegisterTurn(guardianProfileTestThreadID, "turn-1", nil, 0, &turn.TurnStartParams{
		ThreadID: guardianProfileTestThreadID,
		Config: map[string]any{
			"features": map[string]any{"guardian_enhanced_node_repl_transcripts": true},
		},
	}); err != nil {
		t.Fatalf("RegisterTurn: %v", err)
	}
	enhancedSelection := router.guardianReviewTranscriptSelection(guardianProfileTestThreadID)
	enhancedOutput := guardianTranscriptOutputText(t, enhancedSelection)
	if enhancedOutput != oversized {
		t.Fatalf("enhanced selection must keep the complete node_repl output, got %d bytes of %d", len(enhancedOutput), len(oversized))
	}
}

// Mirrors Rust #38397/#57176 evidence collection: the reviewed turn's feature
// flags decide whether completed node_repl results are retained for Guardian
// review, and the turn is resolved from the running turn rather than dropped.
func TestRecordNodeReplReviewEvidenceUsesReviewedTurnLikeRust(t *testing.T) {
	execution := &turn.ToolExecutionResult{
		Invocation: &tool.Invocation{
			CallID:   "call-1",
			ToolName: tool.ToolName{Namespace: "node_repl", Name: "js"},
			Source:   "code_mode",
		},
		Output: &tool.Output{
			Success: true,
			Data: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "exported 42"}},
			},
		},
	}

	disabled := guardianProfileTestRouter(t, nil)
	disabled.recordNodeReplReviewEvidence(guardianProfileTestThreadID, "turn-1", execution)
	if fragment := disabled.guardianReviewNodeReplEvidence(guardianProfileTestThreadID, 0); fragment != nil {
		t.Fatalf("disabled evidence mode retained a fragment: %q", fragment.Body())
	}

	enabled := guardianProfileTestRouter(t, nil)
	if err := enabled.threads.RegisterTurn(guardianProfileTestThreadID, "turn-1", nil, 0, &turn.TurnStartParams{
		ThreadID: guardianProfileTestThreadID,
		Config: map[string]any{
			"features": map[string]any{"guardian_enhanced_node_repl_transcripts": true},
		},
	}); err != nil {
		t.Fatalf("RegisterTurn: %v", err)
	}
	enabled.recordNodeReplReviewEvidence(guardianProfileTestThreadID, "turn-1", execution)
	fragment := enabled.guardianReviewNodeReplEvidence(guardianProfileTestThreadID, 0)
	if fragment == nil {
		t.Fatal("enabled evidence mode dropped the recorded node_repl result")
	}
	if body := fragment.Body(); !strings.Contains(body, "exported 42") {
		t.Fatalf("evidence body missing the recorded result: %q", body)
	}
}

func guardianTranscriptOutputText(t *testing.T, selection guardianTranscriptSelection) string {
	t.Helper()
	for _, entry := range selection.Entries {
		if strings.HasSuffix(entry.LineRole, " result") {
			return entry.Text
		}
	}
	t.Fatalf("no node_repl tool output entry in %#v", selection.Entries)
	return ""
}
