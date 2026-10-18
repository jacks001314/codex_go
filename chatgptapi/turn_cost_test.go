package chatgptapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloudClientGetTurnCostsUsesBothEndpointStylesLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name     string
		basePath string
		wantPath string
	}{
		{name: "codex api", wantPath: "/api/codex/usage/thread-estimates/query"},
		{name: "chatgpt api", basePath: "/backend-api", wantPath: "/backend-api/wham/usage/thread-estimates/query"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requestBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != tc.wantPath {
					t.Errorf("request = %s %s, want POST %s", r.Method, r.URL.Path, tc.wantPath)
					http.NotFound(w, r)
					return
				}
				if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"threads":[{"thread_id":"thread-1","turns":[{"turn_id":"turn-1","model":"mock-model","estimated_usage_usd_micros":1250001,"settled_response_ids":["resp-cost"]}]}]}`))
			}))
			defer server.Close()

			client := NewCloudClient(&CloudClientOptions{
				BaseURL:    server.URL + tc.basePath,
				HTTPClient: server.Client(),
			})
			costs, err := client.GetTurnCosts(context.Background(), map[string][]string{"thread-1": {"turn-1"}})
			if err != nil {
				t.Fatalf("GetTurnCosts() error = %v", err)
			}
			if len(costs) != 1 || costs[0].ThreadID != "thread-1" || len(costs[0].Turns) != 1 {
				t.Fatalf("GetTurnCosts() = %#v", costs)
			}
			turn := costs[0].Turns[0]
			if turn.TurnID != "turn-1" || turn.EstimatedUsageUSDMicros == nil || *turn.EstimatedUsageUSDMicros != 1250001 {
				t.Fatalf("turn cost = %#v", turn)
			}
			if len(turn.SettledResponseIDs) != 1 || turn.SettledResponseIDs[0] != "resp-cost" {
				t.Fatalf("settled response ids = %#v", turn.SettledResponseIDs)
			}
			if requestBody["include_settled_response_ids"] != true {
				t.Fatalf("include_settled_response_ids = %#v", requestBody["include_settled_response_ids"])
			}
			threads, _ := requestBody["threads"].([]any)
			if len(threads) != 1 {
				t.Fatalf("threads = %#v", requestBody["threads"])
			}
			thread, _ := threads[0].(map[string]any)
			if thread["thread_id"] != "thread-1" {
				t.Fatalf("thread request = %#v", thread)
			}
			turnIDs, _ := thread["turn_ids"].([]any)
			if len(turnIDs) != 1 || turnIDs[0] != "turn-1" {
				t.Fatalf("turn ids = %#v", thread["turn_ids"])
			}
		})
	}
}

func TestCloudClientGetTurnCostsPreservesNullableEstimateLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"threads":[{"thread_id":"thread-1","turns":[{"turn_id":"turn-1","estimated_usage_usd_micros":null,"settled_response_ids":[]}]}]}`))
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	costs, err := client.GetTurnCosts(context.Background(), map[string][]string{"thread-1": {"turn-1"}})
	if err != nil {
		t.Fatalf("GetTurnCosts() error = %v", err)
	}
	turn := costs[0].Turns[0]
	if turn.EstimatedUsageUSDMicros != nil {
		t.Fatalf("nullable estimate = %v, want nil", *turn.EstimatedUsageUSDMicros)
	}
	if turn.SettledResponseIDs == nil || len(turn.SettledResponseIDs) != 0 {
		t.Fatalf("empty settled ids = %#v, want a present empty list", turn.SettledResponseIDs)
	}
}
