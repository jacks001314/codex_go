package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
)

// guardianNativeHistoricalCall is the stored shape Rust #57474 feeds through
// incremental reviews: a historical encrypted MAv2 send_message call whose
// original ID, namespace and encryption metadata must reach the reviewer.
func guardianNativeHistoricalCall(namespace string) session.Item {
	raw := map[string]any{
		"type":                    "function_call",
		"id":                      "fc_historical_message",
		"call_id":                 "historical-message",
		"name":                    "send_message",
		"arguments":               `{"target":"missing_worker","message":"opaque-message-request"}`,
		"encrypted_content":       "opaque-call-replay",
		"encrypted_function_args": []any{"message"},
	}
	if namespace != "" {
		raw["namespace"] = namespace
	}
	data, _ := json.Marshal(raw)
	item := session.Item{
		ID:        "historical-message",
		Type:      "function_call",
		Role:      "assistant",
		Name:      "send_message",
		CallID:    "historical-message",
		Text:      `{"target":"missing_worker","message":"opaque-message-request"}`,
		CreatedAt: fixedTime(),
		Metadata:  map[string]any{"rawResponseItem": json.RawMessage(data)},
	}
	if namespace != "" {
		item.Namespace = namespace
	}
	return item
}

// guardianNativeInputMaps flattens one review request into typed input items so
// the test can assert on native response items and text messages alike.
func guardianNativeInputMaps(t *testing.T, items []any) []map[string]any {
	t.Helper()
	parsed := make([]map[string]any, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case json.RawMessage:
			var payload map[string]any
			if err := json.Unmarshal(typed, &payload); err != nil {
				t.Fatalf("unmarshal native input item: %v", err)
			}
			parsed = append(parsed, payload)
		case map[string]any:
			parsed = append(parsed, typed)
		}
	}
	return parsed
}

// Mirrors Rust #57474 (scenarios_guardian_agent_messages_tests.rs): a historical
// encrypted send_message call and the encrypted parent reply reach both
// incremental Guardian reviews exactly once, in order, and the second review
// reuses the reviewer thread. Calls keep their original ID, namespace and
// encryption metadata and are paired with the evidence-only output.
func TestGuardianNativeEncryptedHistoryAcrossReviewsLikeRust(t *testing.T) {
	for _, test := range []struct {
		name      string
		namespace string
	}{
		// The stored default-namespace call carries no explicit namespace.
		{name: "default namespace", namespace: "functions"},
		{name: "custom namespace", namespace: "team"},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := fixedTime()
			parentReply := encryptedAgentMessageRaw("/root", "/root/worker", "sealed-reply")
			storedNamespace := test.namespace
			if storedNamespace == "functions" {
				// The default-namespace call is stored without an explicit field.
				storedNamespace = ""
			}
			store := session.NewStore(t.TempDir())
			if err := store.Create(&session.Record{
				ID:        session.ThreadID(guardianProfileTestThreadID),
				SessionID: guardianProfileTestThreadID,
				CreatedAt: now,
				UpdatedAt: now,
				RecencyAt: now,
				Metadata:  session.Metadata{Source: "cli", ModelProvider: "openai"},
				Items: []session.Item{
					{ID: "user-1", Type: "message", Role: "user", Text: "Check the change after the parent confirms the exact action.", CreatedAt: now},
					guardianNativeHistoricalCall(storedNamespace),
					{ID: "agent-1", Type: "agent_message", CreatedAt: now, Metadata: map[string]any{"rawResponseItem": parentReply}},
				},
			}); err != nil {
				t.Fatalf("store.Create: %v", err)
			}
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
			profile := guardianNativeTestProfile(test.namespace)

			var requests []*model.AgentRequest
			calls := 0
			runner := &guardianSessionRunner{agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
				requests = append(requests, request)
				calls++
				responseID := "review-1"
				if calls > 1 {
					responseID = "review-2"
				}
				return &model.AgentResponse{ResponseID: responseID, Message: guardianDecisionDurationAssessment}, nil
			})}
			reviewer := &modelGuardianReviewer{
				agent: runner,
				store: state.NewReviewStore(),
				transcriptSelection: func(string) guardianTranscriptSelection {
					return router.guardianReviewTranscriptSelectionWithProfile(guardianProfileTestThreadID, profile)
				},
				transcriptFormat: func(string) state.GuardianTranscriptFormat {
					return state.GuardianTranscriptFormatJSON
				},
			}
			action := state.Action{Type: "command", Command: "exit 0", CWD: t.TempDir(), Justification: "Check the requested change."}
			for index, review := range []struct{ turnID, callID string }{
				{turnID: "turn-first-check", callID: "call-first-check"},
				{turnID: "turn-second-check", callID: "call-second-check"},
			} {
				decision, _, err := reviewer.Review(context.Background(), guardianProfileTestThreadID, review.turnID, review.callID, action)
				if err != nil {
					t.Fatalf("review %d error = %v", index+1, err)
				}
				if decision != state.DecisionApproved {
					t.Fatalf("review %d decision = %q, want approved", index+1, decision)
				}
				// Rust's between-review action result extends the transcript. The
				// live store and the independent Guardian history both record it.
				appended := session.Item{ID: "user-2", Type: "message", Role: "user", Text: "Only staging.", CreatedAt: now}
				if _, err := store.AppendItem(session.ThreadID(guardianProfileTestThreadID), appended); err != nil {
					t.Fatalf("append transcript item: %v", err)
				}
				router.recordGuardianReviewHistoryAppend(guardianProfileTestThreadID, []session.Item{appended})
			}
			if len(requests) != 2 {
				t.Fatalf("review requests = %d, want 2", len(requests))
			}
			if got := strings.TrimSpace(requests[0].PreviousResponseID); got != "" {
				t.Fatalf("first review previous response = %q, want empty", got)
			}
			// Successive reviews reuse the same reviewer thread.
			if got := strings.TrimSpace(requests[1].PreviousResponseID); got != "review-1" {
				t.Fatalf("second review previous response = %q, want review-1", got)
			}

			type nativeEvidence struct {
				call   map[string]any
				reply  map[string]any
				output map[string]any
			}
			evidence := make([]nativeEvidence, 0, len(requests))
			for index, request := range requests {
				items := guardianNativeInputMaps(t, request.InputItems)
				callIndex, replyIndex, outputIndex := -1, -1, -1
				callCount, replyCount := 0, 0
				var found nativeEvidence
				for itemIndex, item := range items {
					switch item["type"] {
					case "function_call":
						if item["name"] != "send_message" {
							continue
						}
						callCount++
						callIndex = itemIndex
						found.call = item
					case "function_call_output":
						if item["call_id"] != "historical-message" {
							continue
						}
						outputIndex = itemIndex
						found.output = item
					case "agent_message":
						replyCount++
						replyIndex = itemIndex
						found.reply = item
					}
				}
				if callCount != 1 || replyCount != 1 {
					t.Fatalf("review %d native evidence = %d calls/%d replies, want one of each: %#v", index+1, callCount, replyCount, items)
				}
				if callIndex >= replyIndex {
					t.Fatalf("review %d call index = %d, reply index = %d; want the call before the parent reply", index+1, callIndex, replyIndex)
				}
				if outputIndex != callIndex+1 {
					t.Fatalf("review %d paired output index = %d, call index = %d; want the evidence output immediately after the call", index+1, outputIndex, callIndex)
				}
				if found.call == nil || found.reply == nil || found.output == nil {
					t.Fatalf("review %d missing native evidence: %#v", index+1, items)
				}
				if found.call["call_id"] != "historical-message" || found.call["id"] != "fc_historical_message" {
					t.Fatalf("review %d call identity = %#v", index+1, found.call)
				}
				if found.call["encrypted_content"] != "opaque-call-replay" {
					t.Fatalf("review %d call encryption metadata = %#v", index+1, found.call)
				}
				encryptedArgs, ok := found.call["encrypted_function_args"].([]any)
				if !ok || len(encryptedArgs) != 1 || encryptedArgs[0] != "message" {
					t.Fatalf("review %d encrypted_function_args = %#v", index+1, found.call["encrypted_function_args"])
				}
				namespace, _ := found.call["namespace"].(string)
				if test.namespace == "functions" {
					if strings.TrimSpace(namespace) != "" {
						t.Fatalf("review %d default-namespace call = %#v, want no explicit namespace", index+1, found.call)
					}
				} else if namespace != test.namespace {
					t.Fatalf("review %d namespace = %q, want %q", index+1, namespace, test.namespace)
				}
				if found.output["output"] != state.GuardianNativeEvidenceOutputText {
					t.Fatalf("review %d paired output = %#v", index+1, found.output)
				}
				replyJSON, err := json.Marshal(found.reply)
				if err != nil {
					t.Fatalf("marshal parent reply: %v", err)
				}
				if !strings.Contains(string(replyJSON), "sealed-reply") {
					t.Fatalf("review %d parent reply = %s, want the ciphertext", index+1, replyJSON)
				}
				if strings.Contains(request.Prompt, "sealed-reply") || strings.Contains(request.Prompt, "opaque-message-request") {
					t.Fatalf("review %d flattened native evidence into the prompt: %q", index+1, request.Prompt)
				}
				evidence = append(evidence, found)
				if index == 1 {
					// The second review appends the new turn's evidence instead of
					// dropping or duplicating the earlier history.
					if !strings.Contains(guardianNativePromptText(items), "Only staging.") {
						t.Fatalf("second review did not append the new transcript entry: %#v", items)
					}
				} else if strings.Contains(guardianNativePromptText(items), "Only staging.") {
					t.Fatalf("first review saw later transcript evidence: %#v", items)
				}
			}
			firstCall, _ := json.Marshal(evidence[0].call)
			secondCall, _ := json.Marshal(evidence[1].call)
			if string(firstCall) != string(secondCall) {
				t.Fatalf("historical call changed between reviews: %s vs %s", firstCall, secondCall)
			}
			firstReply, _ := json.Marshal(evidence[0].reply)
			secondReply, _ := json.Marshal(evidence[1].reply)
			if string(firstReply) != string(secondReply) {
				t.Fatalf("parent reply changed between reviews: %s vs %s", firstReply, secondReply)
			}
		})
	}
}

// guardianNativePromptText joins the text input items so a test can look for
// appended transcript evidence without depending on the prompt layout.
func guardianNativePromptText(items []map[string]any) string {
	var builder strings.Builder
	for _, item := range items {
		if item["type"] != "message" {
			continue
		}
		content, _ := item["content"].([]any)
		for _, part := range content {
			partMap, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := partMap["text"].(string); ok {
				builder.WriteString(text)
				builder.WriteByte('\n')
			}
		}
	}
	return builder.String()
}
