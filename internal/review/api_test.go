package review

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStartValidatesThreadID(t *testing.T) {
	service := NewService()
	if _, err := service.Start(nil); err == nil {
		t.Fatalf("expected nil validation error")
	}
	if _, err := service.Start(&StartParams{}); err == nil {
		t.Fatalf("expected validation error")
	}
	response, err := service.Start(&StartParams{ThreadID: "thread-1", Target: APITarget{Type: "diff"}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if response.Turn.ID != "review-thread-1" {
		t.Fatalf("unexpected turn: %#v", response.Turn)
	}
}

func TestReviewTargetCommitTitleRequiredNullable(t *testing.T) {
	data, err := json.Marshal(&APITarget{Type: "commit", SHA: "abc123"})
	if err != nil {
		t.Fatalf("Marshal commit target error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal commit target error = %v", err)
	}
	if payload["type"] != "commit" || payload["sha"] != "abc123" {
		t.Fatalf("commit target = %#v", payload)
	}
	if _, ok := payload["title"]; !ok || payload["title"] != nil {
		t.Fatalf("title should be required nullable: %#v", payload)
	}
}

func TestReviewStartResponseMarshalRustTurnShape(t *testing.T) {
	service := NewService()
	service.SetClock(func() time.Time { return time.Unix(123, 0) })
	response, err := service.Start(&StartParams{ThreadID: "thread-1", Target: APITarget{Type: "uncommittedChanges"}})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal StartResponse error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal StartResponse error = %v", err)
	}
	turnPayload := payload["turn"].(map[string]any)
	if turnPayload["itemsView"] != "notLoaded" || turnPayload["status"] != TurnStatusInProgress {
		t.Fatalf("review turn = %#v", turnPayload)
	}
	if items, ok := turnPayload["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("review turn items = %#v", turnPayload["items"])
	}
	for _, key := range []string{"error", "startedAt", "completedAt", "durationMs"} {
		if _, ok := turnPayload[key]; !ok || turnPayload[key] != nil {
			t.Fatalf("%s should be required nullable: %#v", key, turnPayload)
		}
	}
	if payload["reviewThreadId"] != "thread-1" {
		t.Fatalf("reviewThreadId = %#v", payload["reviewThreadId"])
	}
}
