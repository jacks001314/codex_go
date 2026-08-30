package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/applypatch"
	"codex_go/model"
)

func TestDynamicToolAudioContentJSON(t *testing.T) {
	data, err := json.Marshal(&DynamicToolCallOutputContent{Type: "inputAudio", AudioURL: "data:audio/wav;base64,YXVkaW8="})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"type":"inputAudio","audioUrl":"data:audio/wav;base64,YXVkaW8="}` {
		t.Fatalf("audio content json = %s", data)
	}
}

func TestTokenUsageNotificationMarshalComputesTotal(t *testing.T) {
	contextWindow := int64(128000)
	data, err := json.Marshal(&ThreadTokenUsageUpdatedNotification{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		TokenUsage: TokenUsage{
			InputTokens:           10,
			CachedInputTokens:     2,
			CacheWriteInputTokens: 3,
			OutputTokens:          5,
			ReasoningOutputTokens: 1,
			ModelContextWindow:    &contextWindow,
		},
	})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	var payload struct {
		ThreadID   string `json:"threadId"`
		TurnID     string `json:"turnId"`
		TokenUsage struct {
			Total              TokenUsageBreakdown `json:"total"`
			Last               TokenUsageBreakdown `json:"last"`
			ModelContextWindow *int64              `json:"modelContextWindow"`
		} `json:"tokenUsage"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("UnmarshalJSON() error = %v", err)
	}
	if payload.TokenUsage.Total.TotalTokens != 15 || payload.TokenUsage.Last.TotalTokens != 15 {
		t.Fatalf("payload = %+v", payload.TokenUsage)
	}
	if payload.TokenUsage.Total.CachedInputTokens != 2 || payload.TokenUsage.Total.CacheWriteInputTokens != 3 || payload.TokenUsage.Total.ReasoningOutputTokens != 1 {
		t.Fatalf("payload = %+v", payload.TokenUsage)
	}
	if payload.TokenUsage.ModelContextWindow == nil || *payload.TokenUsage.ModelContextWindow != contextWindow {
		t.Fatalf("modelContextWindow = %#v", payload.TokenUsage.ModelContextWindow)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal raw returned error: %v", err)
	}
	usage, ok := raw["tokenUsage"].(map[string]any)
	if !ok {
		t.Fatalf("tokenUsage = %#v", raw["tokenUsage"])
	}
	for _, legacy := range []string{"inputTokens", "cachedInputTokens", "outputTokens", "reasoningOutputTokens", "totalTokens"} {
		if _, ok := usage[legacy]; ok {
			t.Fatalf("legacy flat token usage field %q should not be emitted: %s", legacy, data)
		}
	}
}

func TestRawResponseCompletedNotificationIncludesCacheWriteTokens(t *testing.T) {
	data, err := json.Marshal(&RawResponseCompletedNotification{
		ThreadID: "thread-1", TurnID: "turn-1", ResponseID: "resp-1",
		Usage: &TokenUsageBreakdown{TotalTokens: 10, InputTokens: 7, CachedInputTokens: 2, CacheWriteInputTokens: 3, OutputTokens: 3},
	})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	if !strings.Contains(string(data), `"cacheWriteInputTokens":3`) || !strings.Contains(string(data), `"responseId":"resp-1"`) {
		t.Fatalf("data = %s", data)
	}
}

func TestRawResponseCompletedNotificationIncludesUsageMetadata(t *testing.T) {
	amount := "0.00123456789"
	data, err := json.Marshal(&RawResponseCompletedNotification{
		ThreadID: "thread-1", TurnID: "turn-1", ResponseID: "resp-1",
		UsageMetadata: &model.ResponseUsageMetadata{Amount: &amount},
	})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	if !strings.Contains(string(data), `"usageMetadata":{"amount":"0.00123456789"}`) {
		t.Fatalf("data = %s", data)
	}
}

func TestWithoutNotificationMediaOmitItemMediaLikeRust(t *testing.T) {
	payload := ThreadItemPayload{
		"type": "message",
		"content": []any{
			map[string]any{"type": "input_text", "text": "hello"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,xyz"},
			map[string]any{"type": "input_audio", "audio_url": "data:audio/wav;base64,abc"},
		},
	}
	notification := withoutNotificationMedia(&ItemStartedNotification{Item: payload}).(*ItemStartedNotification)
	content, ok := notification.Item["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("content = %#v, want only the input_text entry", notification.Item["content"])
	}
	if text := content[0].(map[string]any); text["type"] != "input_text" || text["text"] != "hello" {
		t.Fatalf("first content = %#v", content[0])
	}

	raw := withoutNotificationMedia(&RawResponseItemCompletedNotification{Item: map[string]any{
		"type":   "function_call_output",
		"output": []any{map[string]any{"type": "input_text", "text": "ok"}, map[string]any{"type": "input_image", "image_url": "data:image/png;base64,xyz"}},
	}}).(*RawResponseItemCompletedNotification)
	rawContent := raw.Item.(map[string]any)["output"].([]any)
	if len(rawContent) != 1 || rawContent[0].(map[string]any)["type"] != "input_text" {
		t.Fatalf("raw response item output = %#v", rawContent)
	}

	// #41427: function call output thread items also strip image/audio while
	// preserving text and encrypted content.
	fn := ThreadItemPayload{
		"type":    "function_call_output",
		"call_id": "call-1",
		"output": []any{
			map[string]any{"type": "input_text", "text": "result"},
			map[string]any{"type": "encrypted_content", "encrypted_content": "enc"},
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64,xyz"},
			map[string]any{"type": "input_audio", "audio_url": "data:audio/wav;base64,abc"},
		},
	}
	completed := withoutNotificationMedia(&ItemCompletedNotification{Item: fn}).(*ItemCompletedNotification)
	fnContent := completed.Item["output"].([]any)
	if len(fnContent) != 2 || fnContent[0].(map[string]any)["type"] != "input_text" || fnContent[1].(map[string]any)["type"] != "encrypted_content" {
		t.Fatalf("function call output = %#v", fnContent)
	}
}

func TestContextCompactedNotificationUsesDeprecatedRustShape(t *testing.T) {
	data, err := json.Marshal(&ContextCompactedNotification{
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		Summary:   "summary",
		ItemCount: 2,
	})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	output := string(data)
	if !strings.Contains(output, `"threadId":"thread-1"`) || !strings.Contains(output, `"turnId":"turn-1"`) {
		t.Fatalf("data = %s", data)
	}
	for _, legacy := range []string{"summary", "itemCount", "tokenUsage", "responseId"} {
		if strings.Contains(output, legacy) {
			t.Fatalf("legacy compacted field %q should not be emitted: %s", legacy, data)
		}
	}
}

func TestRustNotificationNullableAndLegacyFields(t *testing.T) {
	warningData, err := json.Marshal(&WarningNotification{Message: "careful"})
	if err != nil {
		t.Fatalf("Marshal warning returned error: %v", err)
	}
	if !strings.Contains(string(warningData), `"threadId":null`) || !strings.Contains(string(warningData), `"message":"careful"`) {
		t.Fatalf("warning payload = %s", warningData)
	}

	deprecationData, err := json.Marshal(&DeprecationNoticeNotification{Message: "legacy summary"})
	if err != nil {
		t.Fatalf("Marshal deprecation returned error: %v", err)
	}
	deprecationOutput := string(deprecationData)
	if !strings.Contains(deprecationOutput, `"summary":"legacy summary"`) || !strings.Contains(deprecationOutput, `"details":null`) {
		t.Fatalf("deprecation payload = %s", deprecationData)
	}
	if strings.Contains(deprecationOutput, `"message"`) {
		t.Fatalf("legacy message should not be emitted: %s", deprecationData)
	}

	goalData, err := json.Marshal(&GoalUpdatedNotification{
		ThreadID: "thread-1",
		Goal: Goal{
			ThreadID:  "thread-1",
			Objective: "finish parity",
			Status:    GoalActive,
		},
	})
	if err != nil {
		t.Fatalf("Marshal goal update returned error: %v", err)
	}
	if !strings.Contains(string(goalData), `"turnId":null`) || !strings.Contains(string(goalData), `"objective":"finish parity"`) {
		t.Fatalf("goal update payload = %s", goalData)
	}

	oauthData, err := json.Marshal(&MCPServerOauthLoginCompletedNotification{
		Name:    "github",
		Success: true,
	})
	if err != nil {
		t.Fatalf("Marshal oauth login completed returned error: %v", err)
	}
	if !strings.Contains(string(oauthData), `"threadId":null`) || strings.Contains(string(oauthData), `"error"`) {
		t.Fatalf("oauth login completed payload = %s", oauthData)
	}

	errorText := "denied"
	oauthErrorData, err := json.Marshal(&MCPServerOauthLoginCompletedNotification{
		Name:    "github",
		Success: false,
		Error:   &errorText,
	})
	if err != nil {
		t.Fatalf("Marshal oauth login completed error returned error: %v", err)
	}
	if !strings.Contains(string(oauthErrorData), `"error":"denied"`) {
		t.Fatalf("oauth login completed error payload = %s", oauthErrorData)
	}
}

func TestModelSafetyBufferingUpdatedNotificationRustShape(t *testing.T) {
	data, err := json.Marshal(&ModelSafetyBufferingUpdatedNotification{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Model:    "gpt-5",
	})
	if err != nil {
		t.Fatalf("Marshal model safety buffering returned error: %v", err)
	}
	output := string(data)
	for _, want := range []string{
		`"threadId":"thread-1"`,
		`"turnId":"turn-1"`,
		`"model":"gpt-5"`,
		`"useCases":[]`,
		`"reasons":[]`,
		`"showBufferingUi":false`,
		`"fasterModel":null`,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("model safety buffering payload missing %s: %s", want, data)
		}
	}
}

func TestThreadRealtimeNotificationsUseRustWireShape(t *testing.T) {
	startedData, err := json.Marshal(&ThreadRealtimeStartedNotification{
		ThreadID:          "thread-1",
		Version:           "v2",
		RealtimeSessionID: nil,
	})
	if err != nil {
		t.Fatalf("Marshal realtime started returned error: %v", err)
	}
	startedOutput := string(startedData)
	if !strings.Contains(startedOutput, `"realtimeSessionId":null`) || strings.Contains(startedOutput, `"sessionId"`) {
		t.Fatalf("realtime started payload = %s", startedData)
	}

	audioData, err := json.Marshal(&ThreadRealtimeOutputAudioDeltaNotification{
		ThreadID: "thread-1",
		Audio: ThreadRealtimeAudioChunk{
			Data:        "AA==",
			NumChannels: 1,
			SampleRate:  24000,
		},
	})
	if err != nil {
		t.Fatalf("Marshal realtime audio returned error: %v", err)
	}
	audioOutput := string(audioData)
	if !strings.Contains(audioOutput, `"itemId":null`) || !strings.Contains(audioOutput, `"samplesPerChannel":null`) {
		t.Fatalf("realtime audio payload = %s", audioData)
	}
}

func TestServerRequestResolvedNotificationWireShape(t *testing.T) {
	data, err := json.Marshal(&ServerRequestResolvedNotification{
		ThreadID:  "thread-1",
		RequestID: StringID("request-1"),
		Outcome:   map[string]any{"legacy": true},
	})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	output := string(data)
	if !strings.Contains(output, `"threadId":"thread-1"`) || !strings.Contains(output, `"requestId":"request-1"`) {
		t.Fatalf("data = %s", data)
	}
	if strings.Contains(output, "outcome") {
		t.Fatalf("legacy outcome should not be emitted: %s", data)
	}
}

func TestTerminalInteractionNotificationRustShape(t *testing.T) {
	data, err := json.Marshal(&TerminalInteractionNotification{
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		ItemID:    "item-1",
		ProcessID: "proc-1",
		Stdin:     "echo hi",
		Data:      map[string]any{"legacy": true},
	})
	if err != nil {
		t.Fatalf("Marshal terminal interaction returned error: %v", err)
	}
	output := string(data)
	if !strings.Contains(output, `"stdin":"echo hi"`) || strings.Contains(output, `"data"`) {
		t.Fatalf("terminal interaction payload = %s", data)
	}
}

func TestMCPServerStatusUpdatedNotificationRustStatusShape(t *testing.T) {
	threadID := "thread-1"
	reason := MCPServerStartupFailureReauthenticationRequired
	data, err := json.Marshal(&MCPServerStatusUpdatedNotification{
		ThreadID:      &threadID,
		Name:          "server-1",
		Status:        "stopped",
		FailureReason: &reason,
	})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	output := string(data)
	if !strings.Contains(output, `"threadId":"thread-1"`) || !strings.Contains(output, `"status":"cancelled"`) || !strings.Contains(output, `"error":null`) {
		t.Fatalf("status notification should keep Rust v2 shape: %s", data)
	}
	if strings.Contains(output, `"stopped"`) {
		t.Fatalf("internal stopped status should not be emitted: %s", data)
	}
}

func TestFileChangePatchUpdatedNotificationUsesRustChangeShape(t *testing.T) {
	data, err := json.Marshal(&FileChangePatchUpdatedNotification{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		ItemID:   "patch-1",
		Changes: []any{map[string]any{
			"path": "README.md",
			"kind": map[string]any{"type": "update"},
			"diff": "@@\n-old\n+new\n",
		}},
	})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	output := string(data)
	if !strings.Contains(output, `"threadId":"thread-1"`) || !strings.Contains(output, `"move_path":null`) {
		t.Fatalf("file change patchUpdated should keep Rust shape: %s", data)
	}
}

func TestApplyPatchStreamingFileChangeDeleteDiffIsEmpty(t *testing.T) {
	action, err := applypatch.Parse(`*** Begin Patch
*** Delete File: old.txt
*** End Patch`)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	changes := applyPatchActionFileChangeMaps(action)
	if len(changes) != 1 {
		t.Fatalf("changes = %#v", changes)
	}
	if changes[0]["diff"] != "" {
		t.Fatalf("delete streaming diff = %#v, want empty", changes[0]["diff"])
	}
}

func TestApplyPatchPartialStreamingFileChanges(t *testing.T) {
	changes := partialApplyPatchFileChangeMaps(`*** Begin Patch
*** Add File: new.txt
+hello
*** Delete File: old.txt
*** Update File: src/old.txt
*** Move to: src/new.txt
@@
-old
+new`)
	if len(changes) != 3 {
		t.Fatalf("partial changes = %#v", changes)
	}
	if changes[0]["path"] != "new.txt" || changes[0]["diff"] != "hello\n" {
		t.Fatalf("partial add change = %#v", changes[0])
	}
	if changes[1]["path"] != "old.txt" || changes[1]["diff"] != "" {
		t.Fatalf("partial delete change = %#v", changes[1])
	}
	kind := changes[2]["kind"].(map[string]any)
	if changes[2]["path"] != "src/old.txt" || kind["move_path"] != "src/new.txt" || !strings.Contains(changes[2]["diff"].(string), "+new") {
		t.Fatalf("partial update change = %#v", changes[2])
	}
}

func TestResponsesStreamPatchFingerprintDedupesUnchangedChanges(t *testing.T) {
	state := newResponsesStreamNotificationState(false, "turn-1")
	changes := []map[string]any{{"path": "new.txt", "diff": "hello\n"}}
	if !state.rememberPatchFingerprint("patch-1", changes) {
		t.Fatalf("first fingerprint should be new")
	}
	if state.rememberPatchFingerprint("patch-1", changes) {
		t.Fatalf("unchanged fingerprint should be duplicate")
	}
	changes[0]["diff"] = "hello again\n"
	if !state.rememberPatchFingerprint("patch-1", changes) {
		t.Fatalf("changed fingerprint should be new")
	}
}
