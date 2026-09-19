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

// TestBuildResponsesClientMetadataSerializesTheCompactionOperation mirrors
// Rust's turn-metadata serialization for a compaction request: the request kind
// is "compaction" and the operation's dispatch fields travel in the
// `compaction` object, with the strategy defaulted to memento.
func TestBuildResponsesClientMetadataSerializesTheCompactionOperation(t *testing.T) {
	client := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		InstallationID: "install",
		SessionID:      "session",
		ThreadID:       "thread",
		TurnID:         "turn",
		WindowID:       "thread:1",
		RequestKind:    codexapi.ClientRequestCompaction,
		Compaction: &codexapi.ClientCompactionMetadata{
			Trigger:        "auto",
			Reason:         "contextWindowExceeded",
			Implementation: "responses",
			Phase:          "preTurn",
		},
	})
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if turnMetadata["request_kind"] != "compaction" {
		t.Fatalf("request_kind = %#v, want compaction", turnMetadata["request_kind"])
	}
	compaction, _ := turnMetadata["compaction"].(map[string]any)
	if compaction == nil {
		t.Fatalf("compaction metadata = %#v", turnMetadata)
	}
	for key, want := range map[string]any{
		"trigger":        "auto",
		"reason":         "contextWindowExceeded",
		"implementation": "responses",
		"phase":          "preTurn",
		"strategy":       "memento",
	} {
		if compaction[key] != want {
			t.Fatalf("compaction.%s = %#v, want %#v", key, compaction[key], want)
		}
	}
}

// TestBuildResponsesClientMetadataForwardsHistoryIngest mirrors Rust's
// with_window_and_fork_metadata history_ingest_requested flag.
func TestBuildResponsesClientMetadataForwardsHistoryIngest(t *testing.T) {
	client := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		SessionID:              "session",
		ThreadID:               "thread",
		TurnID:                 "turn",
		RequestKind:            codexapi.ClientRequestTurn,
		HistoryIngestRequested: true,
	})
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if turnMetadata[codexapi.HistoryIngestRequestedKey] != true {
		t.Fatalf("turn metadata = %#v, want history_ingest_requested", turnMetadata)
	}
	plain := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		SessionID:   "session",
		ThreadID:    "thread",
		TurnID:      "turn",
		RequestKind: codexapi.ClientRequestTurn,
	})
	var plainMetadata map[string]any
	if err := json.Unmarshal([]byte(plain[codexapi.ClientCodexTurnMetadataHeader]), &plainMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if _, ok := plainMetadata[codexapi.HistoryIngestRequestedKey]; ok {
		t.Fatalf("turn metadata = %#v, want no history_ingest_requested", plainMetadata)
	}
}

func TestBuildResponsesClientMetadataIncludesAnalyticsEnabled(t *testing.T) {
	enabled := true
	client := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		SessionID:        "session",
		ThreadID:         "thread",
		TurnID:           "turn",
		RequestKind:      codexapi.ClientRequestTurn,
		AnalyticsEnabled: &enabled,
		Extra:            map[string]string{"analytics_enabled": "client-supplied"},
	})
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if turnMetadata["analytics_enabled"] != true {
		t.Fatalf("analytics_enabled = %#v, want true (Rust #44628)", turnMetadata["analytics_enabled"])
	}

	disabled := false
	client = BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		SessionID:        "session",
		ThreadID:         "thread",
		TurnID:           "turn",
		RequestKind:      codexapi.ClientRequestTurn,
		AnalyticsEnabled: &disabled,
	})
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if turnMetadata["analytics_enabled"] != false {
		t.Fatalf("analytics_enabled = %#v, want false", turnMetadata["analytics_enabled"])
	}

	client = BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		SessionID:   "session",
		ThreadID:    "thread",
		TurnID:      "turn",
		RequestKind: codexapi.ClientRequestTurn,
	})
	var omitted map[string]any
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &omitted); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if _, ok := omitted["analytics_enabled"]; ok {
		t.Fatalf("analytics_enabled should be omitted without session analytics: %#v", omitted)
	}
}

func TestMetadataStateAnalyticsEnabled(t *testing.T) {
	state := NewMetadataState("session", "thread", "turn")
	if _, ok := state.MetadataValue("model", "")["analytics_enabled"]; ok {
		t.Fatalf("analytics_enabled should be omitted by default")
	}
	enabled := true
	state.SetAnalyticsEnabled(&enabled)
	enabled = false // mutating the caller's value must not change the state
	if got := state.MetadataValue("model", "")["analytics_enabled"]; got != true {
		t.Fatalf("analytics_enabled = %#v, want true", got)
	}
	state.SetAnalyticsEnabled(nil)
	if _, ok := state.MetadataValue("model", "")["analytics_enabled"]; ok {
		t.Fatalf("analytics_enabled should be omitted after clearing")
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

func TestBuildResponsesClientMetadataIncludesAgentNameLikeRust(t *testing.T) {
	client := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		InstallationID: "install",
		SessionID:      "session",
		ThreadID:       "thread",
		TurnID:         "turn",
		RequestKind:    codexapi.ClientRequestTurn,
		AgentName:      "/root/worker",
	})
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata = %q: %v", client[codexapi.ClientCodexTurnMetadataHeader], err)
	}
	if turnMetadata["agent_name"] != "/root/worker" {
		t.Fatalf("agent_name = %#v, want /root/worker", turnMetadata["agent_name"])
	}

	// Root fallback: without an explicit agent path the runtime supplies
	// "/root".
	root := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		SessionID:   "session",
		ThreadID:    "thread",
		TurnID:      "turn",
		RequestKind: codexapi.ClientRequestTurn,
		AgentName:   "/root",
	})
	if err := json.Unmarshal([]byte(root[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("root turn metadata = %q: %v", root[codexapi.ClientCodexTurnMetadataHeader], err)
	}
	if turnMetadata["agent_name"] != "/root" {
		t.Fatalf("root agent_name = %#v", turnMetadata["agent_name"])
	}
}

func TestBuildResponsesClientMetadataIncludesNodeReplPolicy(t *testing.T) {
	required := true
	disabled := false
	client := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		InstallationID:             "install",
		SessionID:                  "session",
		ThreadID:                   "thread",
		TurnID:                     "turn",
		RequestKind:                codexapi.ClientRequestTurn,
		NodeReplAutoReviewRequired: &required,
		NodeReplDisabled:           &disabled,
	})
	var turnMetadata map[string]any
	if err := json.Unmarshal([]byte(client[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if turnMetadata["node_repl_auto_review_required"] != true || turnMetadata["node_repl_disabled"] != false {
		t.Fatalf("node repl metadata = %#v", turnMetadata)
	}

	filtered := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{
		InstallationID: "install",
		SessionID:      "session",
		ThreadID:       "thread",
		TurnID:         "turn",
		RequestKind:    codexapi.ClientRequestTurn,
		Extra: map[string]string{
			"node_repl_auto_review_required": "client-value",
			"node_repl_disabled":             "client-value",
		},
	})
	var filteredMetadata map[string]any
	if err := json.Unmarshal([]byte(filtered[codexapi.ClientCodexTurnMetadataHeader]), &filteredMetadata); err != nil {
		t.Fatalf("filtered turn metadata json error = %v", err)
	}
	if _, ok := filteredMetadata["node_repl_auto_review_required"]; ok {
		t.Fatalf("client-provided node repl metadata leaked: %#v", filteredMetadata)
	}
	if _, ok := filteredMetadata["node_repl_disabled"]; ok {
		t.Fatalf("client-provided node repl disabled leaked: %#v", filteredMetadata)
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

// Mirrors Rust Session::current_window_id: the conversation window identity is
// `{thread_id}:{window_number}` with a zero-based number, and the Responses
// client metadata reports it as `x-codex-window-id`.
func TestConversationWindowIDIsZeroBasedLikeRust(t *testing.T) {
	if got := ConversationWindowID("thread-1", 0); got != "thread-1:0" {
		t.Fatalf("first window = %q, want thread-1:0", got)
	}
	if got := ConversationWindowID("thread-1", 3); got != "thread-1:3" {
		t.Fatalf("advanced window = %q", got)
	}
	if got := ConversationWindowID("  ", 0); got != "" {
		t.Fatalf("thread without an id = %q", got)
	}

	first := uint64(0)
	metadata := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{ThreadID: "thread-1", WindowNumber: &first})
	if metadata[codexapi.ClientCodexWindowIDHeader] != "thread-1:0" {
		t.Fatalf("client metadata window id = %q", metadata[codexapi.ClientCodexWindowIDHeader])
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(metadata[codexapi.ClientCodexTurnMetadataHeader]), &document); err != nil {
		t.Fatalf("turn metadata document = %q (%v)", metadata[codexapi.ClientCodexTurnMetadataHeader], err)
	}
	if document["window_id"] != "thread-1:0" {
		t.Fatalf("turn metadata window_id = %#v", document["window_id"])
	}

	// An explicit window id still wins over the derived default.
	explicit := BuildResponsesClientMetadata(&ResponsesClientMetadataOptions{ThreadID: "thread-1", WindowID: "thread-1:7"})
	if explicit[codexapi.ClientCodexWindowIDHeader] != "thread-1:7" {
		t.Fatalf("explicit window id = %q", explicit[codexapi.ClientCodexWindowIDHeader])
	}
}

// Rust's current_meta_value_for_mcp_request derives the external MCP metadata
// document from the turn metadata: no request identity, no harness-owned
// agent/parent/root turn fields, and the user-input flag only when requested.
func TestMCPTurnMetadataFromResponsesMetadataLikeRust(t *testing.T) {
	raw := `{"session_id":"session","thread_id":"thread","turn_id":"turn-1","installation_id":"install",` +
		`"window_id":"window","window_number":2,"context_window_id":"context","request_kind":"turn",` +
		`"agent_name":"/root/worker","parent_thread_id":"parent-thread","parent_turn_id":"parent-turn",` +
		`"root_turn_id":"root-turn","model":"gpt-5.4","reasoning_effort":"high","codex_version":"1.2.3",` +
		`"sandbox_mode":"workspace-write","turn_started_at_unix_ms":1700,"workspace_kind":"git",` +
		`"tool_namespaces_info":{"functions":{"name":"functions"}}}`

	document := MCPTurnMetadataFromResponsesMetadata(raw, false)
	for _, key := range []string{
		"installation_id", "window_id", "window_number", "context_window_id",
		"request_kind", "compaction", "agent_name", "parent_turn_id", "root_turn_id",
		// The harness-owned tool inventory never reaches an external MCP server.
		"tool_namespaces_info",
	} {
		if _, ok := document[key]; ok {
			t.Fatalf("MCP metadata carries %q: %#v", key, document)
		}
	}
	for key, want := range map[string]any{
		"session_id":              "session",
		"thread_id":               "thread",
		"turn_id":                 "turn-1",
		"parent_thread_id":        "parent-thread",
		"model":                   "gpt-5.4",
		"reasoning_effort":        "high",
		"codex_version":           "1.2.3",
		"sandbox_mode":            "workspace-write",
		"turn_started_at_unix_ms": float64(1700),
		"workspace_kind":          "git",
	} {
		if document[key] != want {
			t.Fatalf("MCP metadata[%q] = %#v, want %#v (%#v)", key, document[key], want, document)
		}
	}
	if _, ok := document["user_input_requested_during_turn"]; ok {
		t.Fatalf("MCP metadata flagged user input the turn never requested: %#v", document)
	}
	if requested := MCPTurnMetadataFromResponsesMetadata(raw, true); requested["user_input_requested_during_turn"] != true {
		t.Fatalf("MCP metadata missing the user-input flag: %#v", requested)
	}
	if document := MCPTurnMetadataFromResponsesMetadata("   ", true); document != nil {
		t.Fatalf("empty metadata produced a document: %#v", document)
	}
	if document := MCPTurnMetadataFromResponsesMetadata("{", true); document != nil {
		t.Fatalf("malformed metadata produced a document: %#v", document)
	}
}
