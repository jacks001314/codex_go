package review

import (
	"encoding/json"
	"strings"
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

func TestStartValidationUsesRustReviewTargetErrors(t *testing.T) {
	service := NewService()
	cases := []struct {
		name   string
		target APITarget
		want   string
	}{
		{name: "base", target: APITarget{Type: "baseBranch", Branch: "  "}, want: "branch must not be empty"},
		{name: "commit", target: APITarget{Type: "commit", SHA: "  "}, want: "sha must not be empty"},
		{name: "custom", target: APITarget{Type: "custom", Instructions: "  "}, want: "instructions must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := service.Start(&StartParams{ThreadID: "thread-1", Target: tc.target})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReviewStartDisplayHintsMatchRust(t *testing.T) {
	title := " Fix bug "
	cases := []struct {
		name   string
		target APITarget
		want   string
	}{
		{name: "uncommitted", target: APITarget{Type: "uncommittedChanges"}, want: "current changes"},
		{name: "base", target: APITarget{Type: "baseBranch", Branch: "main"}, want: "changes against 'main'"},
		{name: "commit title", target: APITarget{Type: "commit", SHA: "abcdef123456", Title: &title}, want: "commit abcdef1: Fix bug"},
		{name: "custom", target: APITarget{Type: "custom", Instructions: " check auth "}, want: "check auth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UserFacingHintForTarget(tc.target.ToTarget()); got != tc.want {
				t.Fatalf("hint = %q, want %q", got, tc.want)
			}
		})
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
	title := " Fix bug "
	response, err := service.Start(&StartParams{ThreadID: "thread-1", Target: APITarget{Type: "commit", SHA: "abcdef123456", Title: &title}})
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
	items, ok := turnPayload["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("review turn items = %#v", turnPayload["items"])
	}
	userMessage, ok := items[0].(map[string]any)
	if !ok || userMessage["type"] != "userMessage" || userMessage["id"] != "review-thread-1" || userMessage["clientId"] != nil {
		t.Fatalf("review display item = %#v", items[0])
	}
	content, ok := userMessage["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("review display content = %#v", userMessage["content"])
	}
	text, ok := content[0].(map[string]any)
	if !ok || text["type"] != "text" || text["text"] != "commit abcdef1: Fix bug" {
		t.Fatalf("review display text = %#v", content[0])
	}
	if elements, ok := text["text_elements"].([]any); !ok || len(elements) != 0 {
		t.Fatalf("review text elements = %#v", text["text_elements"])
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
