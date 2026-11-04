package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

func additionalContextHarnessFlag(t *testing.T, item session.Item) bool {
	t.Helper()
	var metadata map[string]any
	switch raw := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		if err := json.Unmarshal(raw, &metadata); err != nil {
			t.Fatalf("unmarshal harness metadata: %v", err)
		}
	case string:
		if err := json.Unmarshal([]byte(raw), &metadata); err != nil {
			t.Fatalf("unmarshal harness metadata: %v", err)
		}
	case map[string]any:
		metadata = raw
	default:
		t.Fatalf("harness metadata = %#v", item.Data["harness_metadata"])
	}
	flagged, _ := metadata["additional_context_tool_output"].(bool)
	return flagged
}

func TestAdditionalContextToolOutputProvenanceLikeRust(t *testing.T) {
	now := fixedTime()
	router := &RuntimeRouter{}
	_, modelItems, persisted := router.mergeAdditionalContext("thread", map[string]turn.AdditionalContextEntry{
		"browser_info": {Kind: turn.AdditionalContextUntrusted, Value: "tab one"},
	}, true)
	if len(modelItems) != 1 || len(persisted) != 1 {
		t.Fatalf("rendered = %#v, persisted = %#v", modelItems, persisted)
	}
	modelItem := modelItems[0].(map[string]any)
	if _, present := modelItem["harness_metadata"]; present {
		t.Fatalf("acting-model payload contains harness metadata: %#v", modelItem)
	}
	if modelItem["type"] != "function_call_output" || modelItem["name"] != "browser_info" || modelItem["namespace"] != "agent" || modelItem["output"] != "tab one" {
		t.Fatalf("acting-model payload = %#v", modelItem)
	}

	items := additionalContextSessionItems("turn-1", persisted, now)
	if len(items) != 1 || !additionalContextHarnessFlag(t, items[0]) {
		t.Fatalf("persisted items = %#v", items)
	}
	replayed := session.InputItemsFromItems(items, &session.HistoryBuildOptions{IncludeToolOutputs: true})
	if len(replayed) != 1 {
		t.Fatalf("replayed items = %#v", replayed)
	}
	replayedItem := replayed[0].(map[string]any)
	if _, present := replayedItem["harness_metadata"]; present {
		t.Fatalf("replayed payload leaked harness metadata: %#v", replayedItem)
	}
	for _, key := range []string{"type", "name", "namespace", "output"} {
		if replayedItem[key] != modelItem[key] {
			t.Fatalf("replayed %s = %#v, want %#v", key, replayedItem[key], modelItem[key])
		}
	}
	if _, present := replayedItem["id"]; present {
		t.Fatalf("replayed payload added an id: %#v", replayedItem)
	}

	home := t.TempDir()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: "thread", SessionID: "thread", Source: "cli",
		CWD: home, ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := rollout.AppendSessionItems(recorder, items, now); err != nil {
		t.Fatalf("AppendSessionItems() error = %v", err)
	}
	path := recorder.Path()
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	restoredItems := rollout.ReviewHistoryItems(lines, now)
	if len(restoredItems) != 1 || !guardianAdditionalContextToolOutput(restoredItems[0]) {
		t.Fatalf("restored items = %#v", restoredItems)
	}
	if restoredItems[0].Text != "tab one" {
		t.Fatalf("restored output = %q", restoredItems[0].Text)
	}
}

func TestGuardianAdditionalContextPolicyLikeRust(t *testing.T) {
	for _, include := range []bool{true, false} {
		t.Run(map[bool]string{true: "include", false: "exclude"}[include], func(t *testing.T) {
			configText := "[features]\nuntrusted_as_tool_output = true\n"
			if !include {
				configText += "guardian_include_additional_context = false\n"
			}
			router, _, _, store := newAdditionalContextTestRouter(t, configText)
			threadID := session.ThreadID("123e4567-e89b-42d3-a456-426614174151")
			now := fixedTime()
			_, _, rendered := router.mergeAdditionalContext(string(threadID), map[string]turn.AdditionalContextEntry{
				"browser_info": {Kind: turn.AdditionalContextUntrusted, Value: "tab one"},
			}, true)
			contextItems := additionalContextSessionItems("turn-1", rendered, now)
			ordinary := session.Item{
				ID: "ordinary-output", Type: "function_call_output", Name: "browser_info",
				Namespace: "agent", Text: "ordinary tool evidence",
				Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: now,
			}
			record := &session.Record{
				ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
				Metadata: session.Metadata{Source: "cli", ModelProvider: "openai"},
				Items: append(append([]session.Item{{
					ID: "request", Type: "message", Role: "user", Text: "inspect context",
					Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: now,
				}}, contextItems...), ordinary),
			}
			if err := store.Create(record); err != nil {
				t.Fatalf("store.Create() error = %v", err)
			}
			items := router.guardianReviewHistoryItems(string(threadID), record)
			var joined strings.Builder
			for _, item := range items {
				joined.WriteString(item.Text)
				joined.WriteByte('\n')
			}
			text := joined.String()
			if !strings.Contains(text, "inspect context") || !strings.Contains(text, "ordinary tool evidence") {
				t.Fatalf("review history lost ordinary evidence:\n%s", text)
			}
			if got := strings.Contains(text, "tab one"); got != include {
				t.Fatalf("additional context included = %v, want %v:\n%s", got, include, text)
			}
		})
	}
}
