package retainedctx

import (
	"encoding/json"
	"testing"
)

// TestHarnessMetadataTypedMembersDecodeLikeRust pins the typed view of
// `CodexHarnessMetadata`: every member Rust persists with a response item
// decodes under its exact wire name, including the retained delivery evidence
// the Guardian composition deduplicates against.
func TestHarnessMetadataTypedMembersDecodeLikeRust(t *testing.T) {
	raw := `{
		"guardian_sources": [{
			"id": {"message_id": "msg-1", "turn_id": "turn-1", "role": "user"},
			"revision": "retained_abc",
			"complete": true
		}],
		"guardian_source_order_guidance": true,
		"retained_source": {
			"id": {"message_id": "msg-2", "turn_id": "turn-1", "role": "assistant"},
			"revision": "retained_def",
			"complete": true
		},
		"client_authored": true,
		"fallback_token_limit_override": 24000,
		"delivered_assistant_message": "the delivered text",
		"harness_authored_configuration": true,
		"compaction_model_hash": "gpt-5-hash",
		"user_input_order": 7,
		"compaction_output": true,
		"inherited_user_message": true,
		"sender_user_messages": {"receiver_turn_id": "turn-1", "receiver_message_id": "msg-3", "text": "Sender: root\n"}
	}`
	var metadata HarnessMetadata
	if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(metadata.GuardianSources) != 1 {
		t.Fatalf("guardian_sources = %#v, want one source", metadata.GuardianSources)
	}
	source := metadata.GuardianSources[0]
	if source.ID.MessageID != "msg-1" || source.ID.TurnID != "turn-1" || source.ID.Role != RetainedSourceRoleUser {
		t.Fatalf("guardian source id = %#v", source.ID)
	}
	if source.Revision != "retained_abc" || !source.Complete {
		t.Fatalf("guardian source = %#v", source)
	}
	if !metadata.GuardianSourceOrderGuidance {
		t.Fatal("guardian_source_order_guidance = false, want true")
	}
	if metadata.RetainedSource == nil || metadata.RetainedSource.ID.MessageID != "msg-2" || metadata.RetainedSource.ID.Role != RetainedSourceRoleAssistant {
		t.Fatalf("retained_source = %#v", metadata.RetainedSource)
	}
	if !metadata.ClientAuthored {
		t.Fatal("client_authored = false, want true")
	}
	if metadata.HistoryTruncationTokenLimit == nil || *metadata.HistoryTruncationTokenLimit != 24000 {
		t.Fatalf("fallback_token_limit_override = %v, want 24000", metadata.HistoryTruncationTokenLimit)
	}
	if metadata.DeliveredAssistantMessage == nil || *metadata.DeliveredAssistantMessage != "the delivered text" {
		t.Fatalf("delivered_assistant_message = %v", metadata.DeliveredAssistantMessage)
	}
	if !metadata.HarnessAuthoredConfiguration {
		t.Fatal("harness_authored_configuration = false, want true")
	}
	if metadata.CompactionModelHash == nil || *metadata.CompactionModelHash != "gpt-5-hash" {
		t.Fatalf("compaction_model_hash = %v", metadata.CompactionModelHash)
	}
	if metadata.UserInputOrder == nil || *metadata.UserInputOrder != 7 {
		t.Fatalf("user_input_order = %v, want 7", metadata.UserInputOrder)
	}
	if !metadata.CompactionOutput {
		t.Fatal("compaction_output = false, want true")
	}
	if !metadata.InheritedUserMessage {
		t.Fatal("inherited_user_message = false, want true")
	}
	if metadata.SenderUserMessages == nil || metadata.SenderUserMessages.Text != "Sender: root\n" {
		t.Fatalf("sender_user_messages = %#v", metadata.SenderUserMessages)
	}
}

// TestHarnessMetadataWireNamesMatchRust pins the two renamed members: the
// history budget travels as `fallback_token_limit_override` and an
// unrecognized spelling is ignored like any other unknown key.
func TestHarnessMetadataWireNamesMatchRust(t *testing.T) {
	var metadata HarnessMetadata
	if err := json.Unmarshal([]byte(`{"history_truncation_token_limit": 5}`), &metadata); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if metadata.HistoryTruncationTokenLimit != nil {
		t.Fatalf("history_truncation_token_limit should be ignored, got %v", *metadata.HistoryTruncationTokenLimit)
	}
}

// TestMcpAttributionCheckpointHandlesMissingOrUnknownIdentity mirrors Rust's
// `mcp_checkpoint_handles_missing_or_unknown_identity`: an absent checkpoint
// stays absent, while a source that carries an identity this build does not
// know makes the whole checkpoint an attribution error with no sources.
func TestMcpAttributionCheckpointHandlesMissingOrUnknownIdentity(t *testing.T) {
	var empty HarnessMetadata
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatalf("Unmarshal empty: %v", err)
	}
	if empty.McpAttribution != nil {
		t.Fatalf("absent mcp_attribution = %#v, want nil", empty.McpAttribution)
	}

	var restored HarnessMetadata
	raw := `{"mcp_attribution":{"status":"complete","sources":[{
		"server_name":"example","tool_name":"search","first_turn_id":"turn_1",
		"future_identity":"unknown to this reader"}]}}`
	if err := json.Unmarshal([]byte(raw), &restored); err != nil {
		t.Fatalf("Unmarshal checkpoint: %v", err)
	}
	want := McpAttributionStatusAttributionError
	if restored.McpAttribution == nil || restored.McpAttribution.Status != want {
		t.Fatalf("mcp_attribution = %#v, want status %q", restored.McpAttribution, want)
	}
	if restored.McpAttribution.ErrorReason != nil || len(restored.McpAttribution.Sources) != 0 {
		t.Fatalf("attribution error fallback = %#v, want no reason and no sources", restored.McpAttribution)
	}
}

// TestMcpAttributionErrorDiagnosticsDoNotInvalidateCheckpoints mirrors Rust's
// `mcp_error_diagnostics_do_not_invalidate_checkpoints`: a missing reason stays
// missing, a future reason decodes as `unknown`, and a non-string reason is
// dropped, all without failing the checkpoint.
func TestMcpAttributionErrorDiagnosticsDoNotInvalidateCheckpoints(t *testing.T) {
	historyMissing := McpAttributionErrorHistoryMissingCheckpoint
	cases := []struct {
		name       string
		reasonJSON string
		want       *McpAttributionErrorReason
	}{
		{"absent", "", nil},
		{"future reason", `,"error_reason":"future_reason"`, reasonPointer(McpAttributionErrorUnknown)},
		{"non-string reason", `,"error_reason":{"invalid":true}`, nil},
		{"known reason", `,"error_reason":"history_missing_checkpoint"`, &historyMissing},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			raw := `{"mcp_attribution":{"status":"attribution_error"` + testCase.reasonJSON + `}}`
			var metadata HarnessMetadata
			if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if metadata.McpAttribution == nil || metadata.McpAttribution.Status != McpAttributionStatusAttributionError {
				t.Fatalf("mcp_attribution = %#v, want attribution_error", metadata.McpAttribution)
			}
			got := metadata.McpAttribution.ErrorReason
			switch {
			case testCase.want == nil && got != nil:
				t.Fatalf("error_reason = %q, want nil", *got)
			case testCase.want != nil && (got == nil || *got != *testCase.want):
				t.Fatalf("error_reason = %v, want %q", got, *testCase.want)
			}
		})
	}
}

// TestMcpAttributionNullCheckpointIsAnError pins the Option-vs-null rule: Rust's
// custom deserializer runs for a present `null`, so the field becomes an
// attribution error rather than disappearing.
func TestMcpAttributionNullCheckpointIsAnError(t *testing.T) {
	var metadata HarnessMetadata
	if err := json.Unmarshal([]byte(`{"mcp_attribution":null}`), &metadata); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if metadata.McpAttribution == nil || metadata.McpAttribution.Status != McpAttributionStatusAttributionError {
		t.Fatalf("null mcp_attribution = %#v, want attribution_error", metadata.McpAttribution)
	}
}

// TestMcpAttributionRequiredFields pins the strict half of the checkpoint: a
// missing status or a source without its required identity fails the checkpoint,
// which the harness metadata turns into an attribution error.
func TestMcpAttributionRequiredFields(t *testing.T) {
	for _, raw := range []string{
		`{"mcp_attribution":{}}`,
		`{"mcp_attribution":{"status":"bogus"}}`,
		`{"mcp_attribution":{"status":"complete","unexpected":true}}`,
		`{"mcp_attribution":{"status":"complete","sources":[{"server_name":"s","tool_name":"t"}]}}`,
	} {
		var metadata HarnessMetadata
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
			t.Fatalf("Unmarshal(%s): %v", raw, err)
		}
		if metadata.McpAttribution == nil || metadata.McpAttribution.Status != McpAttributionStatusAttributionError {
			t.Fatalf("%s: mcp_attribution = %#v, want attribution_error", raw, metadata.McpAttribution)
		}
	}
}

// TestMarkRetainedSourcesIncomplete mirrors
// `CodexHarnessMetadata::mark_retained_sources_incomplete`: dropping delivery
// proof also clears the ordering guidance.
func TestMarkRetainedSourcesIncomplete(t *testing.T) {
	metadata := HarnessMetadata{
		GuardianSourceOrderGuidance: true,
		RetainedSource:              &RetainedSource{Complete: true},
		GuardianSources:             []RetainedSource{{Complete: true}, {Complete: true}},
	}
	metadata.MarkRetainedSourcesIncomplete()
	if metadata.GuardianSourceOrderGuidance {
		t.Fatal("guardian_source_order_guidance still true")
	}
	if metadata.RetainedSource == nil || metadata.RetainedSource.Complete {
		t.Fatalf("retained_source = %#v, want complete=false", metadata.RetainedSource)
	}
	for i, source := range metadata.GuardianSources {
		if source.Complete {
			t.Fatalf("guardian_sources[%d] still complete", i)
		}
	}
}

func reasonPointer(reason McpAttributionErrorReason) *McpAttributionErrorReason {
	return &reason
}
