package codexapi

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestClientPromptFormattedInputStripsImageDetailsForLite(t *testing.T) {
	prompt := NewClientPrompt()
	prompt.Input = []ClientResponseItem{{
		Type: "message",
		Role: "user",
		Content: []ClientContentItem{
			{Kind: ClientContentText, Text: "hello"},
			{Kind: ClientContentImage, ImageURL: "data:", Detail: "high"},
		},
	}}
	got := prompt.FormattedInput(true, true)
	if got[0].Content[1].Detail != "" {
		t.Fatalf("FormattedInput(lite) detail = %q, want empty", got[0].Content[1].Detail)
	}
	if prompt.Input[0].Content[1].Detail != "high" {
		t.Fatalf("FormattedInput mutated prompt")
	}
}

func TestClientPromptFormattedInputNormalizesOriginalImageDetail(t *testing.T) {
	prompt := NewClientPrompt()
	prompt.Input = []ClientResponseItem{{
		Type: "message",
		Role: "user",
		Content: []ClientContentItem{
			{Kind: ClientContentText, Text: "hello"},
			{Kind: ClientContentImage, ImageURL: "data:", Detail: "original"},
		},
	}}
	got := prompt.FormattedInput(false, false)
	if got[0].Content[1].Detail != "high" {
		t.Fatalf("FormattedInput(unsupported original) detail = %q, want high", got[0].Content[1].Detail)
	}
	if prompt.Input[0].Content[1].Detail != "original" {
		t.Fatalf("FormattedInput mutated stored prompt detail = %q", prompt.Input[0].Content[1].Detail)
	}
	kept := prompt.FormattedInput(false, true)
	if kept[0].Content[1].Detail != "original" {
		t.Fatalf("FormattedInput(supported original) detail = %q, want original", kept[0].Content[1].Detail)
	}
	low := NewClientPrompt()
	low.Input = []ClientResponseItem{{
		Type: "message",
		Role: "user",
		Content: []ClientContentItem{
			{Kind: ClientContentImage, ImageURL: "data:", Detail: "low"},
		},
	}}
	if got := low.FormattedInput(false, false); got[0].Content[0].Detail != "low" {
		t.Fatalf("FormattedInput(non-original detail) = %q, want low", got[0].Content[0].Detail)
	}
}

func TestClientMetadataClientMetadataAndHeaders(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.TurnID = "turn"
	metadata.RequestKind = ClientRequestTurn
	enabled := true
	metadata.AutoReviewEnabled = &enabled
	nodeReplAutoReviewRequired := true
	metadata.NodeReplAutoReviewRequired = &nodeReplAutoReviewRequired
	nodeReplDisabled := false
	metadata.NodeReplDisabled = &nodeReplDisabled
	metadata.ParentThreadID = "parent"
	metadata.ParentTurnID = "parent-turn"
	metadata.RootTurnID = "root-turn"
	metadata.SubagentHeader = "review"
	metadata.TurnTrigger = "realtime"
	metadata.Extra = map[string]string{"workspace_kind": "git", "thread_id": "bad"}
	client := metadata.ClientMetadata()
	if client[ClientCodexInstallationIDHeader] != "install" || client[ClientCodexWindowIDHeader] != "window" {
		t.Fatalf("ClientMetadata() = %v", client)
	}
	if client[ClientCodexParentThreadIDHeader] != "parent" || client[ClientOpenAISubagentHeader] != "review" {
		t.Fatalf("ClientMetadata() missing compatibility fields: %v", client)
	}
	if client["parent_turn_id"] != "parent-turn" || !strings.Contains(client[ClientCodexTurnMetadataHeader], `"parent_turn_id":"parent-turn"`) {
		t.Fatalf("ClientMetadata() missing parent turn: %v", client)
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"root_turn_id":"root-turn"`) {
		t.Fatalf("ClientMetadata() missing root turn: %v", client)
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"turn_trigger":"realtime"`) {
		t.Fatalf("turn metadata missing turn_trigger: %s", client[ClientCodexTurnMetadataHeader])
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"workspace_kind":"git"`) {
		t.Fatalf("turn metadata missing extra: %s", client[ClientCodexTurnMetadataHeader])
	}
	if strings.Contains(client[ClientCodexTurnMetadataHeader], `"thread_id":"bad"`) {
		t.Fatalf("reserved extra leaked: %s", client[ClientCodexTurnMetadataHeader])
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"auto_review_enabled":true`) {
		t.Fatalf("turn metadata missing auto_review_enabled: %s", client[ClientCodexTurnMetadataHeader])
	}
	if !strings.Contains(client[ClientCodexTurnMetadataHeader], `"node_repl_auto_review_required":true`) ||
		!strings.Contains(client[ClientCodexTurnMetadataHeader], `"node_repl_disabled":false`) {
		t.Fatalf("turn metadata missing node repl policy: %s", client[ClientCodexTurnMetadataHeader])
	}
	headers := metadata.CompatibilityHeaders()
	if headers[ClientCodexTurnMetadataHeader] == "" || headers[ClientCodexWindowIDHeader] != "window" {
		t.Fatalf("CompatibilityHeaders() = %v", headers)
	}
}

func TestClientReservedMetadataKeysIncludeAutoReviewEnabled(t *testing.T) {
	if !ClientReservedMetadataKeys()["auto_review_enabled"] {
		t.Fatal("auto_review_enabled must be a reserved metadata key (Rust f2a6f2585c)")
	}
	if !ClientReservedMetadataKeys()["root_turn_id"] {
		t.Fatal("root_turn_id must be a reserved metadata key")
	}
	filtered := ClientFilterExtraMetadata(map[string]string{"auto_review_enabled": "client-value", "workspace_kind": "git"})
	if _, ok := filtered["auto_review_enabled"]; ok {
		t.Fatalf("client-provided auto_review_enabled leaked: %#v", filtered)
	}
	if filtered["workspace_kind"] != "git" {
		t.Fatalf("non-reserved extra was filtered: %#v", filtered)
	}
}

func TestClientReservedMetadataKeysIncludeNodeReplPolicy(t *testing.T) {
	reserved := ClientReservedMetadataKeys()
	if !reserved[NodeReplAutoReviewRequiredKey] || !reserved[NodeReplDisabledKey] {
		t.Fatalf("node repl policy keys are not reserved: %#v", reserved)
	}
	filtered := ClientFilterExtraMetadata(map[string]string{
		NodeReplAutoReviewRequiredKey: "client-value",
		NodeReplDisabledKey:           "client-value",
		"workspace_kind":              "git",
	})
	if _, ok := filtered[NodeReplAutoReviewRequiredKey]; ok {
		t.Fatalf("client-provided node repl policy leaked: %#v", filtered)
	}
	if _, ok := filtered[NodeReplDisabledKey]; ok {
		t.Fatalf("client-provided node repl disabled leaked: %#v", filtered)
	}
	if filtered["workspace_kind"] != "git" {
		t.Fatalf("non-reserved extra was filtered: %#v", filtered)
	}
}

func TestClientMetadataIncludesCodexVersionInTurnMetadata(t *testing.T) {
	metadata := NewClientMetadata("installation", "session", "thread", "window")
	metadata.RequestKind = ClientRequestTurn
	metadata.CodexVersion = "1.2.3"
	value := metadata.TurnMetadataValue()
	if value["codex_version"] != "1.2.3" {
		t.Fatalf("codex_version = %#v", value["codex_version"])
	}
	if !ClientReservedMetadataKeys()["codex_version"] {
		t.Fatal("codex_version should be reserved metadata")
	}
}

// Rust's ExecutionMetadata::apply_to inserts the issuing step's model and
// reasoning effort into the request metadata extra; both keys survive into the
// turn-metadata JSON and are omitted when the step has no value.
func TestClientMetadataCarriesModelAndReasoningEffort(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.RequestKind = ClientRequestTurn
	metadata.Model = "gpt-5.4"
	metadata.ReasoningEffort = "high"
	value := metadata.TurnMetadataValue()
	if value["model"] != "gpt-5.4" || value["reasoning_effort"] != "high" {
		t.Fatalf("turn metadata = %#v", value)
	}
	encoded, ok := metadata.TurnMetadataJSON()
	if !ok || !strings.Contains(encoded, `"model":"gpt-5.4"`) || !strings.Contains(encoded, `"reasoning_effort":"high"`) {
		t.Fatalf("turn metadata json = %q", encoded)
	}
	if ClientReservedMetadataKeys()["model"] || ClientReservedMetadataKeys()["reasoning_effort"] {
		t.Fatal("model/reasoning_effort must not be reserved metadata keys")
	}

	empty := NewClientMetadata("install", "session", "thread", "window")
	empty.RequestKind = ClientRequestTurn
	emptyValue := empty.TurnMetadataValue()
	if _, ok := emptyValue["model"]; ok {
		t.Fatalf("empty metadata emitted model: %#v", emptyValue)
	}
	if _, ok := emptyValue["reasoning_effort"]; ok {
		t.Fatalf("empty metadata emitted reasoning_effort: %#v", emptyValue)
	}

	// Rust's ExecutionMetadata::apply_to inserts the captured values after the
	// client-provided entries, so a configured model cannot override the step.
	overridden := NewClientMetadata("install", "session", "thread", "window")
	overridden.RequestKind = ClientRequestTurn
	overridden.Model = "gpt-5.4"
	overridden.Extra = map[string]string{"model": "client-model", "workspace_kind": "git"}
	overridden.ResponsesAPIMetadata = map[string]string{"reasoning_effort": "minimal", "other": "kept"}
	overriddenValue := overridden.TurnMetadataValue()
	if overriddenValue["model"] != "gpt-5.4" {
		t.Fatalf("client-provided model overrode the captured step: %#v", overriddenValue)
	}
	if _, ok := overriddenValue["reasoning_effort"]; ok {
		t.Fatalf("unowned reasoning_effort leaked into captured metadata: %#v", overriddenValue)
	}
	if overriddenValue["workspace_kind"] != "git" || overriddenValue["other"] != "kept" {
		t.Fatalf("unrelated extras were dropped: %#v", overriddenValue)
	}
}

// Rust's current_meta_value_for_mcp_request builds the document external MCP
// servers receive: no request identity, no agent/parent/root turn fields, and
// the per-turn user-input flag added only when the model asked for input.
func TestClientMetadataMCPTurnMetadataValue(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.RequestKind = ClientRequestTurn
	metadata.TurnID = "turn-1"
	metadata.ContextWindowID = "context-1"
	windowNumber := uint64(3)
	metadata.WindowNumber = &windowNumber
	metadata.AgentName = "/root/worker"
	metadata.ParentThreadID = "parent-thread"
	metadata.ParentTurnID = "parent-turn"
	metadata.RootTurnID = "root-turn"
	metadata.CodexVersion = "1.2.3"
	metadata.Model = "gpt-5.4"
	metadata.ReasoningEffort = "high"
	metadata.SandboxMode = "workspace-write"
	analyticsEnabled := true
	metadata.AnalyticsEnabled = &analyticsEnabled
	metadata.TurnStartedAtUnixMS = 1700
	metadata.Workspaces = map[string]ClientWorkspaceMetadata{"/work/a": {LatestGitCommitHash: "abc"}}
	metadata.Extra = map[string]string{"workspace_kind": "git"}

	value := metadata.MCPTurnMetadataValue(false)
	for _, key := range []string{
		InstallationIDKey, WindowIDKey, ContextWindowIDKey, "window_number",
		RequestKindKey, CompactionKey,
		AgentNameKey, ParentTurnIDKey, RootTurnIDKey,
	} {
		if _, ok := value[key]; ok {
			t.Fatalf("MCP metadata carries %q: %#v", key, value)
		}
	}
	for key, want := range map[string]any{
		SessionIDKey:           "session",
		ThreadIDKey:            "thread",
		TurnIDKey:              "turn-1",
		ParentThreadIDKey:      "parent-thread",
		SandboxModeKey:         "workspace-write",
		ModelKey:               "gpt-5.4",
		ReasoningEffortKey:     "high",
		"codex_version":        "1.2.3",
		TurnStartedAtUnixMSKey: int64(1700),
		AnalyticsEnabledKey:    true,
		"workspace_kind":       "git",
	} {
		if value[key] != want {
			t.Fatalf("MCP metadata[%q] = %#v, want %#v (%#v)", key, value[key], want, value)
		}
	}
	if _, ok := value["workspaces"]; !ok {
		t.Fatalf("MCP metadata dropped workspaces: %#v", value)
	}
	if _, ok := value[UserInputRequestedDuringTurnKey]; ok {
		t.Fatalf("MCP metadata flagged user input the turn never requested: %#v", value)
	}

	requested := metadata.MCPTurnMetadataValue(true)
	if requested[UserInputRequestedDuringTurnKey] != true {
		t.Fatalf("MCP metadata missing the user-input flag: %#v", requested)
	}
	if _, ok := metadata.TurnMetadataValue()[UserInputRequestedDuringTurnKey]; ok {
		t.Fatal("the responses metadata must not carry the MCP-only user-input flag")
	}
}

// TestClientMetadataHistoryIngestRequested mirrors Rust's
// with_window_and_fork_metadata: only the true value is serialized in the
// Responses document, and the MCP template (which never sets it) is unaffected.
func TestClientMetadataHistoryIngestRequested(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.RequestKind = ClientRequestTurn
	metadata.HistoryIngestRequested = true
	if metadata.TurnMetadataValue()[HistoryIngestRequestedKey] != true {
		t.Fatalf("turn metadata dropped history_ingest_requested: %#v", metadata.TurnMetadataValue())
	}
	if _, ok := metadata.MCPTurnMetadataValue(false)[HistoryIngestRequestedKey]; ok {
		t.Fatalf("MCP metadata carries history_ingest_requested: %#v", metadata.MCPTurnMetadataValue(false))
	}
	metadata.HistoryIngestRequested = false
	if _, ok := metadata.TurnMetadataValue()[HistoryIngestRequestedKey]; ok {
		t.Fatalf("false history_ingest_requested was serialized: %#v", metadata.TurnMetadataValue())
	}
}

// TestResponsesMetadataReservesTheWindowForkKeys mirrors Rust's
// RESERVED_METADATA_KEYS: product-owned window/fork/ingest keys cannot be
// configured through responses_api_metadata.
func TestResponsesMetadataReservesTheWindowForkKeys(t *testing.T) {
	for _, key := range []string{
		WindowNumberKey,
		ContextWindowIDKey,
		ToolNamespacesInfoKey,
		HistoryIngestRequestedKey,
		ForkedFromOrdinalExclusiveKey,
		GuardianCreditsRequestedKey,
	} {
		if !ClientReservedMetadataKeys()[key] || !reservedMetadataKeys[key] {
			t.Fatalf("%q is not reserved: client=%v responses=%v", key, ClientReservedMetadataKeys()[key], reservedMetadataKeys[key])
		}
		if err := ValidateResponsesAPIMetadata(map[string]string{key: "1"}); err == nil {
			t.Fatalf("responses_api_metadata accepted the reserved key %q", key)
		}
	}
}

func TestClientCompatibilityHeadersOmitUnboundedCodeModeToolNames(t *testing.T) {
	value := map[string]any{
		"thread_id": "thread",
		CodeModeToolNamesKey: map[string]any{
			"view_image": map[string]any{"name": "view_image", "namespace": nil},
		},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	bounded, ok := clientCompatibilityTurnMetadataJSON(value)
	if !ok || strings.Contains(bounded, CodeModeToolNamesKey) || !strings.Contains(bounded, `"thread_id":"thread"`) {
		t.Fatalf("bounded metadata = %q", bounded)
	}
	if !strings.Contains(string(encoded), CodeModeToolNamesKey) {
		t.Fatalf("canonical metadata unexpectedly missing mapping: %s", encoded)
	}
}

func TestClientMetadataMemoryRequestOmitsTurnIdentity(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.RequestKind = ClientRequestMemory
	value := metadata.TurnMetadataValue()
	if _, ok := value["thread_id"]; ok {
		t.Fatalf("memory metadata included thread_id: %v", value)
	}
	if value["request_kind"] != "memory" {
		t.Fatalf("request_kind = %v", value["request_kind"])
	}
}

func TestClientFilterExtraMetadata(t *testing.T) {
	got := ClientFilterExtraMetadata(map[string]string{
		"thread_id":          "bad",
		"parent_turn_id":     "spoofed",
		CodeModeToolNamesKey: "bad",
		"workspace_kind":     "git",
	})
	if got["thread_id"] != "" || got["parent_turn_id"] != "" || got[CodeModeToolNamesKey] != "" || got["workspace_kind"] != "git" {
		t.Fatalf("ClientFilterExtraMetadata() = %v", got)
	}
}

func TestClientRetryStateHandleRetriesFallbackAndFailure(t *testing.T) {
	state := &ClientRetryState{MaxRetries: 2}
	decision := state.Handle(&ClientRetryableError{Message: "stream", RequestedDelay: time.Second}, ClientRetrySampling, false, true, false)
	if !decision.Retry || decision.Delay != time.Second || decision.NotifyUser {
		t.Fatalf("first retry decision = %#v", decision)
	}
	decision = state.Handle(errors.New("stream"), ClientRetrySampling, false, true, false)
	if !decision.Retry || !decision.NotifyUser {
		t.Fatalf("second retry decision = %#v", decision)
	}
	decision = state.Handle(errors.New("stream"), ClientRetrySampling, true, true, false)
	if !decision.Retry || !decision.Fallback || state.Retries != 0 {
		t.Fatalf("fallback decision = %#v state=%#v", decision, state)
	}
	state.UsedFallback = true
	state.Retries = 2
	decision = state.Handle(errors.New("final"), ClientRetrySampling, true, true, false)
	if decision.Error == nil {
		t.Fatalf("final decision error = nil")
	}
}

func TestClientBackoff(t *testing.T) {
	if ClientBackoff(1) != 100*time.Millisecond || ClientBackoff(3) != 400*time.Millisecond {
		t.Fatalf("Backoff values unexpected")
	}
}

func TestClientSubagentHeaderValue(t *testing.T) {
	cases := map[string]string{
		"subagent:review":                 "review",
		"subagent:thread_spawn":           "collab_spawn",
		"subagent_review":                 "review",
		"subagent_thread_spawn_parent_d2": "collab_spawn",
		"subagent_guardian":               "guardian",
		"internal:memory_consolidation":   "memory_consolidation",
		"internal_memory_consolidation":   "memory_consolidation",
		"cli":                             "",
	}
	for input, want := range cases {
		if got := ClientSubagentHeaderValue(input); got != want {
			t.Fatalf("ClientSubagentHeaderValue(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClientSubagentMetadataKind(t *testing.T) {
	cases := map[string]string{
		"subagent:review":                 "review",
		"subagent:thread_spawn":           "thread_spawn",
		"subagent_thread_spawn_parent_d2": "thread_spawn",
		"subagent_guardian":               "guardian",
		"internal_memory_consolidation":   "",
		"cli":                             "",
	}
	for input, want := range cases {
		if got := ClientSubagentMetadataKind(input); got != want {
			t.Fatalf("ClientSubagentMetadataKind(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClientCompactionMetadataDefaults(t *testing.T) {
	metadata := &ClientCompactionMetadata{}
	metadata.EnsureDefaults()
	if metadata.Strategy != "memento" {
		t.Fatalf("Strategy = %q, want memento", metadata.Strategy)
	}
}

func TestClientMetadataSerializesWindowNumberLikeRust(t *testing.T) {
	metadata := NewClientMetadata("install", "session", "thread", "window")
	metadata.RequestKind = ClientRequestTurn
	metadata.ThreadID = "thread"
	metadata.TurnID = "turn"
	windowNumber := uint64(3)
	metadata.WindowNumber = &windowNumber
	client := metadata.ClientMetadata()
	raw, ok := client[ClientCodexTurnMetadataHeader]
	if !ok {
		t.Fatalf("turn metadata header missing")
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("unmarshal turn metadata: %v", err)
	}
	if got, present := decoded["window_number"]; !present || got != float64(3) {
		t.Fatalf("window_number = %#v, want 3", decoded["window_number"])
	}
	// Unset window number is omitted.
	plain := NewClientMetadata("install", "session", "thread", "window")
	plain.RequestKind = ClientRequestTurn
	plain.ThreadID = "thread"
	plain.TurnID = "turn"
	raw2 := plain.ClientMetadata()[ClientCodexTurnMetadataHeader]
	var decoded2 map[string]any
	if err := json.Unmarshal([]byte(raw2), &decoded2); err != nil {
		t.Fatalf("unmarshal plain turn metadata: %v", err)
	}
	if _, present := decoded2["window_number"]; present {
		t.Fatalf("unset window_number should be omitted: %#v", decoded2)
	}
}
