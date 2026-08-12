package turn

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"codex_go/codexapi"
)

func TestTimingStateRecordsTTFTAndTTFM(t *testing.T) {
	timing := NewTimingState()
	start := time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC)
	if got := timing.MarkTurnStarted(start); got != start.UnixMilli() {
		t.Fatalf("MarkTurnStarted() = %d, want %d", got, start.UnixMilli())
	}
	if secs, ok := timing.StartedAtUnixSecs(); !ok || secs != start.Unix() {
		t.Fatalf("StartedAtUnixSecs() = %d/%v", secs, ok)
	}
	if duration, ok := timing.RecordTTFT(start.Add(150 * time.Millisecond)); !ok || duration != 150*time.Millisecond {
		t.Fatalf("RecordTTFT() = %s/%v", duration, ok)
	}
	if _, ok := timing.RecordTTFT(start.Add(time.Second)); ok {
		t.Fatalf("RecordTTFT(second) ok = true")
	}
	if duration, ok := timing.RecordTTFM(start.Add(2 * time.Second)); !ok || duration != 2*time.Second {
		t.Fatalf("RecordTTFM() = %s/%v", duration, ok)
	}
	completed, durationMS, ok := timing.CompletedAtAndDuration(start.Add(3 * time.Second))
	if !ok || completed != start.Add(3*time.Second).Unix() || durationMS != 3000 {
		t.Fatalf("CompletedAtAndDuration() = %d/%d/%v", completed, durationMS, ok)
	}
}

func TestTimingProfile(t *testing.T) {
	timing := NewTimingState()
	start := time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC)
	timing.MarkTurnStarted(start)
	guard := timing.BeginSampling(start.Add(100 * time.Millisecond))
	timing.mu.Lock()
	timing.profile.endPhase(start.Add(400*time.Millisecond), phaseSampling)
	guard.active = false
	timing.mu.Unlock()
	timing.RecordSamplingRetry()
	tool := timing.BeginToolBlocking(start.Add(500 * time.Millisecond))
	timing.mu.Lock()
	timing.profile.endPhase(start.Add(800*time.Millisecond), phaseToolBlocking)
	tool.active = false
	timing.mu.Unlock()
	profile := timing.CompleteProfile(start.Add(time.Second))
	if profile.BeforeFirstSamplingMS != 100 || profile.SamplingMS != 300 || profile.ToolBlockingMS != 300 || profile.SamplingRequestCount != 1 || profile.SamplingRetryCount != 1 {
		t.Fatalf("profile = %#v", profile)
	}
	again := timing.CompleteProfile(start.Add(2 * time.Second))
	if !reflect.DeepEqual(again, profile) {
		t.Fatalf("CompleteProfile() not stable")
	}
}

func TestMetadataState(t *testing.T) {
	metadata := NewMetadataState("session", "thread", "turn")
	metadata.ForkedFromThreadID = "fork"
	metadata.ParentThreadID = "parent"
	metadata.ParentTurnID = "parent-turn"
	metadata.Sandbox = "workspace-write"
	metadata.SetTurnStartedAtUnixMS(42)
	metadata.MarkUserInputRequestedDuringTurn()
	metadata.SetClientMetadata(map[string]string{
		"workspace_kind":           "git",
		"too_long":                 string(make([]byte, 600)),
		"x-codex-installation-id":  "override",
		"x-codex-parent-thread-id": "override-parent",
		"parent_turn_id":           "override-turn",
		"x-codex-turn-metadata":    "override-metadata",
		"x-openai-subagent":        "override-subagent",
	})
	hasChanges := true
	metadata.AddWorkspace("/repo", WorkspaceMetadata{LatestGitCommitHash: "abc", HasChanges: &hasChanges})
	value := metadata.MetadataValue("gpt-5", "high")
	if value["session_id"] != "session" || value["model"] != "gpt-5" || value["reasoning_effort"] != "high" {
		t.Fatalf("MetadataValue() = %#v", value)
	}
	if value["user_input_requested_during_turn"] != true {
		t.Fatalf("user input marker missing")
	}
	if metadata.WorkspaceKind() != "git" {
		t.Fatalf("WorkspaceKind() = %q", metadata.WorkspaceKind())
	}
	if _, ok := value["too_long"]; ok {
		t.Fatalf("too_long metadata was not filtered")
	}
	if _, ok := value["x-codex-installation-id"]; ok {
		t.Fatalf("reserved client metadata was not filtered: %#v", value)
	}
}

func TestBuildResponsesClientMetadataMergesExtraIntoTurnMetadata(t *testing.T) {
	client := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		InstallationID:     "install",
		SessionID:          "session",
		ThreadID:           "thread",
		TurnID:             "turn",
		WindowID:           "thread:1",
		RequestKind:        codexapi.ClientRequestTurn,
		ForkedFromThreadID: "source-thread",
		ParentThreadID:     "parent-thread",
		ParentTurnID:       "parent-turn",
		SubagentHeader:     "guardian",
		SubagentKind:       "guardian",
		ThreadSource:       "automation",
		SandboxMode:        "workspace-write",
		Extra: map[string]string{
			"workspace_kind":           "git",
			"thread_id":                "bad",
			"x-codex-turn-metadata":    "bad",
			"x-codex-parent-thread-id": "bad",
			"parent_turn_id":           "bad",
			"sandbox_mode":             "client-provided",
		},
		StartedAtMS:      42,
		UseResponsesLite: true,
	})
	if client[codexapi.ClientCodexInstallationIDHeader] != "install" || client["thread_id"] != "thread" || client["turn_id"] != "turn" {
		t.Fatalf("client metadata = %#v", client)
	}
	if client[codexapi.ClientCodexParentThreadIDHeader] != "parent-thread" || client[codexapi.ClientOpenAISubagentHeader] != "guardian" {
		t.Fatalf("lineage compatibility metadata = %#v", client)
	}
	if client["ws_request_header_x_openai_internal_codex_responses_lite"] != "true" {
		t.Fatalf("responses lite metadata = %#v", client)
	}
	if client["workspace_kind"] != "" {
		t.Fatalf("extra leaked as top-level client metadata: %#v", client)
	}
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if turnMetadata["workspace_kind"] != "git" || turnMetadata["thread_id"] != "thread" || turnMetadata["turn_started_at_unix_ms"].(float64) != 42 {
		t.Fatalf("turn metadata = %#v", turnMetadata)
	}
	if turnMetadata["sandbox_mode"] != "workspace-write" {
		t.Fatalf("sandbox_mode = %#v, want workspace-write (Rust 4ca25a2c4e)", turnMetadata["sandbox_mode"])
	}
	if turnMetadata["forked_from_thread_id"] != "source-thread" || turnMetadata["parent_thread_id"] != "parent-thread" || turnMetadata["parent_turn_id"] != "parent-turn" || turnMetadata["subagent_kind"] != "guardian" || turnMetadata["thread_source"] != "automation" {
		t.Fatalf("lineage turn metadata = %#v", turnMetadata)
	}
	if turnMetadata["x-codex-parent-thread-id"] != nil {
		t.Fatalf("reserved extra leaked into turn metadata: %#v", turnMetadata)
	}
}

func TestBuildResponsesClientMetadataIncludesAutoReviewEnabled(t *testing.T) {
	enabled := true
	client := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		InstallationID:    "install",
		SessionID:         "session",
		ThreadID:          "thread",
		TurnID:            "turn",
		RequestKind:       codexapi.ClientRequestTurn,
		AutoReviewEnabled: &enabled,
	})
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if turnMetadata["auto_review_enabled"] != true {
		t.Fatalf("auto_review_enabled = %#v, want true (Rust f2a6f2585c)", turnMetadata["auto_review_enabled"])
	}
	// Client-provided values for the reserved key are never accepted.
	filtered := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		InstallationID: "install",
		SessionID:      "session",
		ThreadID:       "thread",
		TurnID:         "turn",
		RequestKind:    codexapi.ClientRequestTurn,
		Extra:          map[string]string{"auto_review_enabled": "client-value"},
	})
	var filteredMetadata map[string]any
	if err := json.Unmarshal([]byte(filtered[codexapi.ClientCodexTurnMetadataHeader]), &filteredMetadata); err != nil {
		t.Fatalf("filtered turn metadata json error = %v", err)
	}
	if _, ok := filteredMetadata["auto_review_enabled"]; ok {
		t.Fatalf("client-provided auto_review_enabled leaked: %#v", filteredMetadata)
	}
}

func TestBudgetStateMaybeReminder(t *testing.T) {
	state := NewBudgetState()
	tokens := int64(50)
	message, ok := state.MaybeReminder(&tokens, &BudgetReminderConfig{ThresholdTokens: 100, Template: "only {tokens_until_compaction} left"})
	if !ok || message != "only 50 left" {
		t.Fatalf("MaybeReminder() = %q/%v", message, ok)
	}
	if _, ok := state.MaybeReminder(&tokens, &BudgetReminderConfig{ThresholdTokens: 100}); ok {
		t.Fatalf("MaybeReminder(second) ok = true")
	}
	high := int64(150)
	if _, ok := NewBudgetState().MaybeReminder(&high, &BudgetReminderConfig{ThresholdTokens: 100}); ok {
		t.Fatalf("MaybeReminder(above threshold) ok = true")
	}
}
