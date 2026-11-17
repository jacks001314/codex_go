package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"codex_go/appserver"
)

// TestReadThreadBoundsPaginatedTurnItemsLikeRust pins Rust #61955: read_thread
// asks for turn metadata without items on paginated threads, loads only the
// newest 20 items per turn through thread/items/list, and returns them in
// chronological order for both includeOutputs settings.
func TestReadThreadBoundsPaginatedTurnItemsLikeRust(t *testing.T) {
	const threadID = "thread-paginated"
	const totalItems = 25
	newestFirst := make([]any, 0, totalItems)
	for index := totalItems - 1; index >= 0; index-- {
		newestFirst = append(newestFirst, map[string]any{"item": map[string]any{
			"id":        fmt.Sprintf("item-%02d", index),
			"type":      "agentMessage",
			"text":      fmt.Sprintf("message %d", index),
			"createdAt": index,
		}})
	}
	newestSecondTurn := make([]any, 0, totalItems)
	for index := totalItems - 1; index >= 0; index-- {
		newestSecondTurn = append(newestSecondTurn, map[string]any{"item": map[string]any{
			"id":        fmt.Sprintf("later-%02d", index),
			"type":      "agentMessage",
			"text":      fmt.Sprintf("later message %d", index),
			"createdAt": index,
		}})
	}
	server := &dynamicToolTestServer{respond: func(method appserver.Method, params json.RawMessage) (any, error) {
		switch method {
		case appserver.MethodThreadRead:
			return map[string]any{"thread": map[string]any{
				"id":          threadID,
				"status":      map[string]any{"type": "idle"},
				"historyMode": "paginated",
				"cwd":         "/repo",
				"createdAt":   1,
				"updatedAt":   5,
			}}, nil
		case appserver.MethodThreadTurnsList:
			var decoded appserver.ThreadTurnsListParams
			if err := json.Unmarshal(params, &decoded); err != nil {
				return nil, err
			}
			if decoded.ItemsView != appserver.TurnItemsNotLoaded {
				return nil, fmt.Errorf("itemsView = %q, want %q", decoded.ItemsView, appserver.TurnItemsNotLoaded)
			}
			return map[string]any{"data": []any{
				map[string]any{"id": "turn-2", "status": "completed", "startedAt": 3, "completedAt": 4},
				map[string]any{"id": "turn-1", "status": "completed", "startedAt": 1, "completedAt": 2},
			}}, nil
		case appserver.MethodThreadItemsList:
			var decoded appserver.ThreadItemsListParams
			if err := json.Unmarshal(params, &decoded); err != nil {
				return nil, err
			}
			if decoded.TurnID == nil {
				return nil, errors.New("turnId is required")
			}
			if decoded.Limit == nil || *decoded.Limit != dynamicReadItemsPerTurn {
				return nil, fmt.Errorf("limit = %v, want %d", decoded.Limit, dynamicReadItemsPerTurn)
			}
			switch *decoded.TurnID {
			case "turn-1":
				return map[string]any{"data": newestFirst[:dynamicReadItemsPerTurn]}, nil
			case "turn-2":
				return map[string]any{"data": newestSecondTurn[:dynamicReadItemsPerTurn]}, nil
			default:
				return nil, fmt.Errorf("unexpected turnId %q", *decoded.TurnID)
			}
		default:
			return nil, errors.New("unexpected method")
		}
	}}
	for _, includeOutputs := range []bool{true, false} {
		response := runDynamicTool(t, server, appserver.DynamicToolCallParams{
			ThreadID: "thread-caller",
			Tool:     "read_thread",
			Arguments: map[string]any{
				"threadId":       threadID,
				"turnLimit":      2,
				"includeOutputs": includeOutputs,
			},
		}, DynamicToolOptions{})
		if !response.Success {
			t.Fatalf("includeOutputs=%v response = %#v", includeOutputs, response)
		}
		payload := dynamicToolText(t, response)
		newest := strings.Index(payload, `"item-24"`)
		oldestKept := strings.Index(payload, `"item-05"`)
		laterKept := strings.Index(payload, `"later-24"`)
		if newest < 0 || oldestKept < 0 || laterKept < 0 {
			t.Fatalf("includeOutputs=%v payload missing bounded items:\n%s", includeOutputs, payload)
		}
		if oldestKept > newest {
			t.Fatalf("includeOutputs=%v items are not chronological:\n%s", includeOutputs, payload)
		}
		for _, dropped := range []string{`"item-04"`, `"item-00"`, `"later-04"`} {
			if strings.Contains(payload, dropped) {
				t.Fatalf("includeOutputs=%v payload retained %s:\n%s", includeOutputs, dropped, payload)
			}
		}
	}
}
