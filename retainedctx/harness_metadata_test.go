package retainedctx

import (
	"encoding/json"
	"strings"
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
		"retain_across_windows": true,
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
	if metadata.RetainAcrossWindows == nil || !*metadata.RetainAcrossWindows {
		t.Fatalf("retain_across_windows = %v, want true", metadata.RetainAcrossWindows)
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

// TestRemovedSearchOwnershipMetadataIsIgnoredLikeRust mirrors Rust #62808:
// older rollouts may still carry `mcp_attribution`, `model_search_state` or
// `native_search_fallback_used`. The metadata model no longer interprets them,
// and a re-encoded item does not carry them again.
func TestRemovedSearchOwnershipMetadataIsIgnoredLikeRust(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"empty", `{}`},
		{"model search state", `{"model_search_state":"search_mcp"}`},
		{"native fallback", `{"model_search_state":"standalone","native_search_fallback_used":true}`},
		{"legacy attribution", `{"mcp_attribution":{"status":"complete","sources":[{"connector_id":"connector_openai_search_service","server_name":"example","tool_name":"search","first_turn_id":"turn_1"}]}}`},
		{"malformed attribution", `{"mcp_attribution":"unreadable"}`},
		{"unknown state", `{"model_search_state":"future_provider"}`},
	}
	removed := []string{"mcp_attribution", "model_search_state", "native_search_fallback_used", "legacy_search_mcp_used"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var metadata HarnessMetadata
			if err := json.Unmarshal([]byte(tc.raw), &metadata); err != nil {
				t.Fatalf("Unmarshal(%s): %v", tc.raw, err)
			}
			encoded, err := json.Marshal(metadata)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			for _, member := range removed {
				if strings.Contains(string(encoded), member) {
					t.Fatalf("serialized metadata kept removed member %q: %s", member, encoded)
				}
			}
		})
	}
}

// TestMarkRetainedSourcesIncomplete mirrors// TestMarkRetainedSourcesIncomplete mirrors
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
