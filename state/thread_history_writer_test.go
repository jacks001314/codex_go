package state

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

func TestPaginatedSessionWriterProjectsCanonicalItemLifecycle(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	now := time.Date(2026, 7, 31, 3, 4, 5, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: "thread-writer", ThreadID: "thread-writer",
		HistoryMode: "paginated", ModelProvider: "openai", CWD: home, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	first := session.Item{
		ID: "command-start", Type: "commandExecution", CallID: "call-1", CreatedAt: now.Add(time.Second),
		Data:     map[string]any{"command": []string{"echo", "ok"}, "cwd": home, "status": "inProgress"},
		Metadata: map[string]any{"turnId": "turn-1", "startedAtMs": now.Add(time.Second).UnixMilli()},
	}
	completed := session.Item{
		ID: "command-end", Type: "commandExecution", CallID: "call-1", Text: "ok\n", CreatedAt: now.Add(2 * time.Second),
		Data:     map[string]any{"command": []string{"echo", "ok"}, "cwd": home, "status": "completed", "exitCode": int64(0)},
		Metadata: map[string]any{"turnId": "turn-1", "startedAtMs": now.Add(time.Second).UnixMilli(), "completedAtMs": now.Add(2 * time.Second).UnixMilli()},
	}
	if err := rollout.AppendSessionItems(recorder, []session.Item{first, completed}, now); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnComplete("turn-1", now.Add(3*time.Second), 3_000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}

	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := MaterializeThreadHistory(ctx, db, "thread-writer", path, 0, nil); err != nil {
		t.Fatal(err)
	}
	var rolloutOrdinal, updatedOrdinal, createdAtMS int64
	var itemType, itemJSON string
	if err := db.QueryRowContext(ctx, `SELECT rollout_ordinal, updated_at_ordinal, created_at_ms, item_type, item_json
FROM thread_items WHERE thread_id = ? AND turn_id = ? AND item_id = ?`, "thread-writer", "turn-1", "call-1").Scan(&rolloutOrdinal, &updatedOrdinal, &createdAtMS, &itemType, &itemJSON); err != nil {
		t.Fatal(err)
	}
	if rolloutOrdinal != 3 || updatedOrdinal != 3 || createdAtMS != now.Add(time.Second).UnixMilli() || itemType != "commandExecution" {
		t.Fatalf("projected lifecycle = ordinal %d updated %d created %d type %q", rolloutOrdinal, updatedOrdinal, createdAtMS, itemType)
	}
	rolloutData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if startedEvents, completedEvents := strings.Count(string(rolloutData), `"type":"item_started"`), strings.Count(string(rolloutData), `"type":"item_completed"`); startedEvents != 1 || completedEvents != 1 {
		t.Fatalf("item lifecycle events = started %d completed %d", startedEvents, completedEvents)
	}
	var public map[string]any
	if err := json.Unmarshal([]byte(itemJSON), &public); err != nil {
		t.Fatal(err)
	}
	if public["type"] != "commandExecution" || public["id"] != "call-1" || public["status"] != "completed" || public["aggregatedOutput"] != "ok\n" || public["exitCode"] != float64(0) {
		t.Fatalf("projected public item = %#v", public)
	}
}

// TestPaginatedSessionWriterCapsOversizedMCPResultsLikeRust mirrors Rust
// #50458's `paginated_mcp_history_caps_large_results`: the projected
// `thread_items.item_json` stores a bounded text preview, keeps the tool
// arguments, and drops structured content and metadata.
func TestPaginatedSessionWriterCapsOversizedMCPResultsLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime, err := InitStateRuntime(ctx, mustSQLiteConfig(t, home), "openai")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	now := time.Date(2026, 7, 31, 4, 5, 6, 0, time.UTC)
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, SessionID: "thread-mcp", ThreadID: "thread-mcp",
		HistoryMode: "paginated", ModelProvider: "openai", CWD: home, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnStarted("turn-1", now); err != nil {
		t.Fatal(err)
	}
	arguments := map[string]any{"query": "large result"}
	item := session.Item{
		ID: "mcp-1", Type: "function_call", Name: "large_result", CallID: "mcp-1", CreatedAt: now.Add(time.Second),
		Data: map[string]any{
			"mcpToolCall": true,
			"server":      "test",
			"tool":        "large_result",
			"arguments":   arguments,
			"status":      "failed",
			"result": map[string]any{
				"content": []any{map[string]any{
					"type": "text",
					"text": "head\n" + strings.Repeat("x", 200_000) + "\ntail",
				}},
				"structuredContent": map[string]any{"x": strings.Repeat("y", 5_000)},
				"_meta":             map[string]any{"note": "meta-tail"},
			},
		},
		Metadata: map[string]any{
			"turnId":        "turn-1",
			"startedAtMs":   now.Add(time.Second).UnixMilli(),
			"completedAtMs": now.Add(2 * time.Second).UnixMilli(),
		},
	}
	if err := rollout.AppendSessionItems(recorder, []session.Item{item}, now); err != nil {
		t.Fatal(err)
	}
	if err := recorder.AppendTurnComplete("turn-1", now.Add(3*time.Second), 3_000); err != nil {
		t.Fatal(err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
		t.Fatal(err)
	}
	db, err := runtime.ThreadHistoryDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := MaterializeThreadHistory(ctx, db, "thread-mcp", path, 0, nil); err != nil {
		t.Fatal(err)
	}
	var itemJSON string
	if err := db.QueryRowContext(ctx, `SELECT item_json FROM thread_items WHERE thread_id = ? AND item_id = ?`,
		"thread-mcp", "mcp-1").Scan(&itemJSON); err != nil {
		t.Fatal(err)
	}
	var projected map[string]any
	if err := json.Unmarshal([]byte(itemJSON), &projected); err != nil {
		t.Fatal(err)
	}
	if projected["type"] != "mcpToolCall" || projected["status"] != "failed" {
		t.Fatalf("projected MCP item = %#v", projected)
	}
	if !reflect.DeepEqual(projected["arguments"], arguments) {
		t.Fatalf("arguments = %#v, want %#v", projected["arguments"], arguments)
	}
	result, _ := projected["result"].(map[string]any)
	if result == nil {
		t.Fatalf("result missing from projected item: %#v", projected)
	}
	if result["structuredContent"] != nil || result["_meta"] != nil {
		t.Fatalf("structured content/meta retained: %#v", result)
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %#v, want one preview", content)
	}
	preview, _ := content[0].(map[string]any)["text"].(string)
	if len(preview) >= 65*1024 {
		t.Fatalf("preview len = %d, want < 65 KiB", len(preview))
	}
	for _, want := range []string{"head", "tail", "chars truncated"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q: %q", want, preview)
		}
	}
}

func mustSQLiteConfig(t *testing.T, home string) SqliteConfig {
	t.Helper()
	config, err := NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	return config
}
