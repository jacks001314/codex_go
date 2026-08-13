package chatgptapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodeTaskDetailsExtractsDiffMessagesPromptAndError(t *testing.T) {
	payload := []byte(`{
		"current_user_turn": {
			"input_items": [{
				"type": "message",
				"role": "user",
				"content": ["First line", {"content_type":"text","text":"Second line"}]
			}]
		},
		"current_assistant_turn": {
			"output_items": [{"type": "message", "content": [{"content_type":"text","text":"Assistant response"}]}],
			"worklog": {"messages": [{"author":{"role":"assistant"}, "content":{"parts":["Worklog response"]}}]},
			"error": {"code":"APPLY_FAILED", "message":"Patch could not be applied"}
		},
		"current_diff_task_turn": {
			"output_items": [{"type": "output_diff", "diff": "diff --git a/lib.rs b/lib.rs"}]
		}
	}`)
	var details CodeTaskDetailsResponse
	if err := json.Unmarshal(payload, &details); err != nil {
		t.Fatalf("unmarshal details: %v", err)
	}
	if diff, ok := details.UnifiedDiff(); !ok || diff != "diff --git a/lib.rs b/lib.rs" {
		t.Fatalf("diff = %q %v", diff, ok)
	}
	messages := details.AssistantTextMessages()
	if len(messages) != 2 || messages[0] != "Assistant response" || messages[1] != "Worklog response" {
		t.Fatalf("messages = %#v", messages)
	}
	if prompt, ok := details.UserTextPrompt(); !ok || prompt != "First line\n\nSecond line" {
		t.Fatalf("prompt = %q %v", prompt, ok)
	}
	if message, ok := details.AssistantErrorMessage(); !ok || message != "APPLY_FAILED: Patch could not be applied" {
		t.Fatalf("error = %q %v", message, ok)
	}
}

func TestWorkspaceMessageUnknownType(t *testing.T) {
	var response WorkspaceMessagesResponse
	if err := json.Unmarshal([]byte(`{"messages":[{"message_id":"m","message_type":"new","message_body":"body"}]}`), &response); err != nil {
		t.Fatalf("unmarshal workspace messages: %v", err)
	}
	if response.Messages[0].MessageType != WorkspaceMessageUnknown {
		t.Fatalf("message type = %s", response.Messages[0].MessageType)
	}
}

func TestCloudClientSendAddCreditsNudgeEmail(t *testing.T) {
	var requestBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/accounts/send_add_credits_nudge_email" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	client := NewCloudClient(&CloudClientOptions{
		BaseURL: server.URL,
		Headers: http.Header{
			"Authorization": []string{"Bearer token"},
		},
	})

	err := client.SendAddCreditsNudgeEmail(context.Background(), AddCreditsNudgeUsageLimit)
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || !statusErr.IsStatus(http.StatusTooManyRequests) {
		t.Fatalf("error = %v, want HTTPStatusError 429", err)
	}
	if requestBody["credit_type"] != "usage_limit" {
		t.Fatalf("request body = %#v", requestBody)
	}
}

func TestCloudClientConsumeRateLimitResetCredit(t *testing.T) {
	var requestBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/codex/rate-limit-reset-credits/consume" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode request body error = %v", err)
		}
		writeJSONChatGPTAPI(t, w, map[string]any{"code": "nothing_to_reset", "windows_reset": 0})
	}))
	defer server.Close()
	client := NewCloudClient(&CloudClientOptions{
		BaseURL: server.URL,
		Headers: http.Header{
			"Authorization": []string{"Bearer token"},
		},
	})

	response, err := client.ConsumeRateLimitResetCredit(context.Background(), " request-1 ")
	if err != nil {
		t.Fatalf("ConsumeRateLimitResetCredit() error = %v", err)
	}
	if response.Code != ConsumeNothingToReset {
		t.Fatalf("response = %+v", response)
	}
	if requestBody["redeem_request_id"] != "request-1" {
		t.Fatalf("request body = %#v", requestBody)
	}
}

func TestCloudClientAccountBackendReads(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/codex/profiles/me":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"stats": map[string]any{
					"lifetime_tokens":     12,
					"daily_usage_buckets": []map[string]any{{"start_date": "2026-05-29", "tokens": 3}},
				},
			})
		case "/api/codex/workspace-messages":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"messages": []map[string]any{{"message_id": "m1", "message_type": "headline", "message_body": "hello"}},
			})
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL})

	profile, err := client.GetTokenUsageProfile(context.Background())
	if err != nil {
		t.Fatalf("GetTokenUsageProfile error = %v", err)
	}
	if profile.Stats.LifetimeTokens == nil || *profile.Stats.LifetimeTokens != 12 || profile.Stats.DailyUsageBuckets == nil || len(*profile.Stats.DailyUsageBuckets) != 1 {
		t.Fatalf("profile = %+v", profile)
	}
	messages, err := client.ListWorkspaceMessages(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaceMessages error = %v", err)
	}
	if len(messages.Messages) != 1 || messages.Messages[0].MessageType != WorkspaceMessageHeadline {
		t.Fatalf("messages = %+v", messages)
	}
	if len(paths) != 2 || paths[0] != "/api/codex/profiles/me" || paths[1] != "/api/codex/workspace-messages" {
		t.Fatalf("paths = %#v", paths)
	}
}

func TestCloudClientGetThreadUsageMatchesRust(t *testing.T) {
	var method, body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		var requestBody []byte
		if r.Body != nil {
			defer r.Body.Close()
			requestBody, _ = io.ReadAll(r.Body)
		}
		body = string(requestBody)
		switch r.URL.Path {
		case "/api/codex/usage/thread_usage/query":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"threads": []map[string]any{{
					"thread_id":                      "thread-1",
					"estimated_usage_credits_micros": 1234,
					"estimated_usage_usd_micros":     567,
					"groups": []map[string]any{{
						"model":                          "gpt-5.2-codex",
						"reasoning_effort":               "high",
						"speed":                          "default",
						"estimated_usage_credits_micros": 1234,
						"net_new_input_tokens":           10,
						"cached_input_tokens":            20,
						"input_tokens":                   30,
						"output_tokens":                  40,
						"total_tokens":                   70,
					}},
				}},
			})
		case "/api/codex/usage/thread_usage/query/wrong":
			_ = json.NewEncoder(w).Encode(map[string]any{"threads": []map[string]any{}})
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL})

	usage, err := client.GetThreadUsage(context.Background(), "thread-1")
	if err != nil {
		t.Fatalf("GetThreadUsage error = %v", err)
	}
	if method != http.MethodPost || body != `{"thread_ids":["thread-1"]}` {
		t.Fatalf("request = %s %s", method, body)
	}
	if usage.ThreadID != "thread-1" || usage.EstimatedUsageCreditsMicros != 1234 || usage.EstimatedUsageUSDMicros == nil || *usage.EstimatedUsageUSDMicros != 567 {
		t.Fatalf("usage = %+v", usage)
	}
	if len(usage.Groups) != 1 || usage.Groups[0].Model == nil || *usage.Groups[0].Model != "gpt-5.2-codex" || usage.Groups[0].OutputTokens == nil || *usage.Groups[0].OutputTokens != 40 {
		t.Fatalf("groups = %+v", usage.Groups)
	}

	if _, err := client.GetThreadUsage(context.Background(), "missing"); err == nil || !strings.Contains(err.Error(), "did not contain requested thread") {
		t.Fatalf("missing thread error = %v", err)
	}
}

func TestCodeTaskTurnAttemptExtractsDiffMessagesAndMetadata(t *testing.T) {
	payload := []byte(`{
		"id": "turn-2",
		"attempt_placement": 2,
		"created_at": "2026-06-30T01:02:03Z",
		"turn_status": "completed",
		"output_items": [
			{"type": "message", "content": ["second attempt"]},
			{"type": "output_diff", "diff": "diff --git a/main.go b/main.go"}
		]
	}`)
	var turn CodeTaskTurn
	if err := json.Unmarshal(payload, &turn); err != nil {
		t.Fatalf("unmarshal turn: %v", err)
	}
	attempt, ok := turn.attempt()
	if !ok {
		t.Fatal("attempt ok = false")
	}
	if attempt.TurnID != "turn-2" || attempt.AttemptPlacement == nil || *attempt.AttemptPlacement != 2 {
		t.Fatalf("attempt metadata = %#v", attempt)
	}
	if attempt.CreatedAt == nil || attempt.CreatedAt.Format("2006-01-02T15:04:05Z07:00") != "2026-06-30T01:02:03Z" {
		t.Fatalf("CreatedAt = %v", attempt.CreatedAt)
	}
	if attempt.Status != CloudAttemptStatusCompleted {
		t.Fatalf("Status = %s", attempt.Status)
	}
	if attempt.Diff == nil || *attempt.Diff != "diff --git a/main.go b/main.go" {
		t.Fatalf("Diff = %v", attempt.Diff)
	}
	if len(attempt.Messages) != 1 || attempt.Messages[0] != "second attempt" {
		t.Fatalf("Messages = %#v", attempt.Messages)
	}
}

func writeJSONChatGPTAPI(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}
