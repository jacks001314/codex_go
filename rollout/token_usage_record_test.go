package rollout

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCompactionCheckpointCarriesTokenUsageRecordLikeRust(t *testing.T) {
	recorder, err := NewRecorder(&CreateParams{
		CodexHome:     t.TempDir(),
		SessionID:     "session-usage",
		ThreadID:      "thread-usage",
		Source:        "cli",
		CWD:           "/workspace",
		ModelProvider: "openai",
		HistoryMode:   "legacy",
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	defer recorder.Close()
	now := time.Now().UTC()
	usage := TokenUsageRecord{
		ThreadID: "thread-usage", TurnID: "turn-1", SessionID: "session-usage",
		RootTurnID: "turn-1", ResponseID: "resp-1",
		Usage: map[string]any{"total_tokens": 12}, TurnTokenUsage: map[string]any{"total_tokens": 12},
		ThreadTokenUsage: map[string]any{"total_tokens": 12},
	}
	if err := recorder.AppendTokenUsageRecord(usage, now); err != nil {
		t.Fatalf("AppendTokenUsageRecord() error = %v", err)
	}
	window := uint64(2)
	if err := recorder.AppendCompactedWithCheckpoint("summary", nil, nil, nil, &window, "compact-response-1", &usage, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendCompactedWithCheckpoint() error = %v", err)
	}

	lines, _, err := Load(recorder.Path())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	latest := LatestTokenUsageRecord(lines)
	if latest == nil || latest.ResponseID != "resp-1" {
		t.Fatalf("latest token usage = %#v", latest)
	}
	var compacted *CompactedEvent
	for i := range lines {
		if lines[i].Type != "compacted" {
			continue
		}
		compacted = compactedEventFromPayload(lines[i].Payload)
	}
	if compacted == nil || compacted.CompactionResponseID == nil || *compacted.CompactionResponseID != "compact-response-1" {
		t.Fatalf("compacted response id = %#v", compacted)
	}
	if compacted.LatestTokenUsageRecord == nil || compacted.LatestTokenUsageRecord.ResponseID != "resp-1" {
		t.Fatalf("compacted latest usage = %#v", compacted.LatestTokenUsageRecord)
	}
	payload, err := json.Marshal(compacted)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["compaction_response_id"]; !ok {
		t.Fatalf("checkpoint wire missing compaction_response_id: %s", payload)
	}
	if _, ok := wire["latest_token_usage_record"]; !ok {
		t.Fatalf("checkpoint wire missing latest_token_usage_record: %s", payload)
	}
}
