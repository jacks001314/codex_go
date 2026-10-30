package app

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"codex_go/appserver"
)

// TestDynamicToolResponsePreservesWaitTargetsLikeRust mirrors Rust #47100:
// an oversized wait response keeps every target and the beginning of each
// answer while dropping supporting metadata.
func TestDynamicToolResponsePreservesWaitTargetsLikeRust(t *testing.T) {
	answer := strings.Repeat("\"\\\n🦀", 10_000)
	polls := make([]any, 0, dynamicMaxWaitTargets)
	for index := 0; index < dynamicMaxWaitTargets; index++ {
		polls = append(polls, map[string]any{
			"schemaVersion": 1,
			"thread":        map[string]any{"id": "thread-" + string(rune('0'+index)), "status": map[string]any{"type": "idle"}},
			"cursor":        "cursor",
			"revision":      123,
			"changed":       true,
			"latestTurn":    map[string]any{"id": "turn", "status": "failed", "error": map[string]any{"message": "Execution failed"}},
			"latestAssistantMessage": map[string]any{
				"id": "answer", "text": answer, "phase": "final_answer",
			},
		})
	}
	response := dynamicToolSuccessResponse(map[string]any{
		"timedOut": false,
		"wake":     map[string]any{"threadId": "thread-0"},
		"polls":    polls,
		"errors":   []any{},
	})
	text := dynamicToolText(t, response)
	if len(text) > dynamicMaxResponseBytes {
		t.Fatalf("response bytes = %d, want <= %d", len(text), dynamicMaxResponseBytes)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, text)
	}
	if output["truncated"] != true {
		t.Fatalf("response not marked truncated: %s", text)
	}
	gotPolls, ok := output["polls"].([]any)
	if !ok || len(gotPolls) != dynamicMaxWaitTargets {
		t.Fatalf("polls = %#v, want %d entries", output["polls"], dynamicMaxWaitTargets)
	}
	for index, rawPoll := range gotPolls {
		poll, _ := rawPoll.(map[string]any)
		if poll["truncated"] != true {
			t.Fatalf("poll %d not marked truncated: %#v", index, poll)
		}
		message, _ := poll["latestAssistantMessage"].(map[string]any)
		retained, _ := message["text"].(string)
		if retained == "" || !strings.HasPrefix(answer, retained) {
			t.Fatalf("poll %d retained answer = %q", index, retained)
		}
	}
}

// TestDynamicToolResponsePreservesListEntriesLikeRust mirrors Rust #47100: the
// compacted list keeps every returned entry so the pagination cursor remains
// valid.
func TestDynamicToolResponsePreservesListEntriesLikeRust(t *testing.T) {
	threads := make([]any, 0, 50)
	for index := 0; index < 50; index++ {
		threads = append(threads, map[string]any{
			"id":      "thread-" + string(rune('A'+index)),
			"kind":    "codex",
			"title":   strings.Repeat("Title", 400),
			"summary": strings.Repeat("Summary", 100),
			"status":  "idle",
			"cwd":     strings.Repeat("/large/path", 100),
		})
	}
	response := dynamicToolSuccessResponse(map[string]any{"threads": threads, "nextCursor": "next-page"})
	text := dynamicToolText(t, response)
	if len(text) > dynamicMaxResponseBytes {
		t.Fatalf("response bytes = %d, want <= %d", len(text), dynamicMaxResponseBytes)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if output["nextCursor"] != "next-page" {
		t.Fatalf("nextCursor = %#v", output["nextCursor"])
	}
	gotThreads, ok := output["threads"].([]any)
	if !ok || len(gotThreads) != 50 {
		t.Fatalf("threads = %d, want 50", len(gotThreads))
	}
	for index, rawThread := range gotThreads {
		thread, _ := rawThread.(map[string]any)
		if thread["id"] != threads[index].(map[string]any)["id"] {
			t.Fatalf("thread %d id = %#v", index, thread["id"])
		}
	}
}

// TestDynamicToolResponsePreservesReadAnswerLikeRust mirrors Rust #47100: a
// read page larger than the budget drops supporting history but keeps the
// assistant answer intact for the next model request.
func TestDynamicToolResponsePreservesReadAnswerLikeRust(t *testing.T) {
	answer := strings.Repeat("Persisted assistant output", 120)
	turn := map[string]any{
		"id":     "turn-1",
		"status": "completed",
		"items": []any{
			map[string]any{"type": "functionCall", "id": "padding", "text": strings.Repeat("supporting arguments", 4_000)},
			map[string]any{"type": "agentMessage", "id": "persisted-message", "phase": "final_answer", "text": answer},
		},
	}
	response := dynamicToolSuccessResponse(map[string]any{
		"thread": map[string]any{"id": "thread-1", "status": "idle"},
		"page":   map[string]any{"order": "newest_first"},
		"turns":  []any{turn},
	})
	text := dynamicToolText(t, response)
	if len(text) > dynamicMaxResponseBytes {
		t.Fatalf("response bytes = %d, want <= %d", len(text), dynamicMaxResponseBytes)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if output["truncated"] != true {
		t.Fatalf("response not marked truncated: %s", text)
	}
	turns, _ := output["turns"].([]any)
	if len(turns) != 1 {
		t.Fatalf("turns = %#v", output["turns"])
	}
	items, _ := turns[0].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("compacted items = %#v", items)
	}
	item, _ := items[0].(map[string]any)
	if item["id"] != "persisted-message" || item["text"] != answer || item["phase"] != "final_answer" {
		t.Fatalf("retained answer = %#v", item)
	}
	if output["page"].(map[string]any)["order"] != "newest_first" {
		t.Fatalf("page = %#v", output["page"])
	}
}

// TestDynamicItemSummaryKeepsFullAssistantAnswerLikeRust ensures the read tool
// no longer truncates an assistant message before the response budget is
// applied (Rust #47100).
func TestDynamicItemSummaryKeepsFullAssistantAnswerLikeRust(t *testing.T) {
	text := strings.Repeat("answer ", 1_000)
	summary := dynamicItemSummary(appserver.ThreadItem{
		ID:   "message-1",
		Type: "agentMessage",
		Text: text,
		Data: map[string]any{"phase": "final_answer"},
	}, false, dynamicDefaultOutputChars)
	if summary["text"] != text {
		t.Fatalf("agent message text was pre-truncated to %d chars", utf8.RuneCountInString(summary["text"].(string)))
	}
	if summary["phase"] != "final_answer" {
		t.Fatalf("phase = %#v", summary["phase"])
	}
}
