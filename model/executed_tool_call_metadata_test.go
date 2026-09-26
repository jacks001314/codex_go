package model

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors Rust #44336: tool-result metadata is bounded, redacted, and serialized
// as the raw snapshot or the omission marker.
func TestToolResultMetadataBoundsAndRedacts(t *testing.T) {
	var zero ToolResultMetadata
	if !zero.IsNone() || zero.IsSome() {
		t.Fatal("zero metadata must be absent")
	}

	snapshot := map[string]any{"provider/custom": map[string]any{"items": []any{1, nil}}}
	metadata := NewToolResultMetadata(snapshot)
	if !metadata.IsSome() {
		t.Fatal("bounded snapshot must be present")
	}
	if metadata.String() != "ToolResultMetadata([redacted])" {
		t.Fatalf("String() = %q, want redacted", metadata.String())
	}
	call := NewExecutedToolCall("tool", map[string]any{})
	if !call.SetToolResultMetadata(metadata) {
		t.Fatal("SetToolResultMetadata must report present metadata")
	}
	encoded, err := json.Marshal(call)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"tool_result_metadata":{"provider/custom":{"items":[1,null]}}`) {
		t.Fatalf("Marshal() = %s", encoded)
	}

	// Rust's `ToolResultMetadata::new` captures the complete snapshot; the
	// outgoing-request budget sheds it later, so capture must not omit it.
	oversized := NewToolResultMetadata(strings.Repeat("x", MaxExecutedToolCallMetadataBytes+1))
	if !oversized.IsSome() || oversized.isOmittedDueToSizeLimit() {
		t.Fatal("capture must retain the raw oversized snapshot")
	}
	call.SetToolResultMetadata(oversized)
	encoded, err = json.Marshal(call)
	if err != nil {
		t.Fatalf("Marshal(oversized) error = %v", err)
	}
	if strings.Contains(string(encoded), "omitted_due_to_size_limit") {
		t.Fatal("capture shed the oversized snapshot before the request budget")
	}
}

// Mirrors Rust's `omit_if_smaller` / `is_omitted_due_to_size_limit`: the marker
// carries the shed overage, replaces the snapshot only when it is smaller, and
// both the bare and overage forms parse as an omission.
func TestToolResultMetadataOmissionMarkerLikeRust(t *testing.T) {
	oversized := ToolResultMetadata{value: strings.Repeat("m", 2_000)}
	if retained := oversized.omitIfSmaller(2_000, 117); retained >= 2_000 {
		t.Fatalf("retained bytes = %d, want the smaller marker", retained)
	}
	if !oversized.isOmittedDueToSizeLimit() {
		t.Fatalf("marker not recognized: %#v", oversized.value)
	}
	if value, _ := oversized.value.(string); value != "omitted_due_to_size_limit (overage_bytes=117)" {
		t.Fatalf("marker = %q", value)
	}
	// An already-small snapshot is never replaced by a larger marker.
	small := ToolResultMetadata{value: map[string]any{}}
	if retained := small.omitIfSmaller(jsonSize(small), 10); retained != jsonSize(small) {
		t.Fatalf("small snapshot retained %d bytes, want %d", retained, jsonSize(small))
	}
	if small.isOmittedDueToSizeLimit() {
		t.Fatal("a small snapshot must not become a marker")
	}
	for _, value := range []string{"omitted_due_to_size_limit", "omitted_due_to_size_limit (overage_bytes=5)"} {
		if !(ToolResultMetadata{value: value}).isOmittedDueToSizeLimit() {
			t.Fatalf("marker %q not recognized", value)
		}
	}
	if (ToolResultMetadata{value: "omitted_due_to_size_limit (overage_bytes=x)"}).isOmittedDueToSizeLimit() {
		t.Fatal("a malformed overage must not parse as an omission")
	}
}

// Metadata is shed before source evidence and calls when the prompt budget is
// exceeded, and omission markers themselves are optional.
func TestBoundExecutedToolCallsShedsMetadataBeforeSourcesAndCalls(t *testing.T) {
	item := &AgentItem{Type: "function_call", Name: "tool", CallID: "call", Arguments: `{}`}
	RecordExecutedToolCall(item)
	calls := item.ExecutedToolCalls()
	calls[0].SetToolResultSources(NewToolResultSources([]ToolResultSource{{Type: "document", ID: "R0"}}))
	// Keep the individual snapshot under the limit, but let the call total cross it.
	calls[0].SetToolResultMetadata(NewToolResultMetadata(strings.Repeat("m", MaxExecutedToolCallMetadataBytes-64)))
	if !calls[0].HasToolResultMetadata() {
		t.Fatal("raw snapshot must be retained before bounding")
	}
	item.ReplaceExecutedToolCalls(calls)

	bounded := BoundExecutedToolCallsForPrompt([]any{item})
	if len(bounded) != 1 {
		t.Fatalf("bounded items = %d, want 1", len(bounded))
	}
	boundedItem := bounded[0].(*AgentItem)
	if executedToolCallMetadataBytes(boundedItem) > MaxExecutedToolCallMetadataBytes {
		t.Fatal("bounded metadata exceeds the prompt budget")
	}
	boundedCalls := boundedItem.ExecutedToolCalls()
	if len(boundedCalls) != 1 {
		t.Fatalf("bounded calls = %d, want the original call retained", len(boundedCalls))
	}
	if boundedCalls[0].truncation != nil {
		t.Fatal("shedding metadata must not truncate the call itself")
	}
	if !boundedCalls[0].HasToolResultSources() {
		t.Fatal("source evidence must survive metadata shedding")
	}
	encoded, err := json.Marshal(boundedCalls[0])
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	// Rust's shedding marker carries the overage it removed.
	if !strings.Contains(string(encoded), `"tool_result_metadata":"omitted_due_to_size_limit (overage_bytes=`) {
		t.Fatalf("raw metadata was not replaced by the omission marker: %s", encoded)
	}
}

// Mirrors Rust's `shed_generic_result_metadata`: the largest snapshot is shed
// first (so one large result cannot discard unrelated small results), and a
// snapshot carrying the resource-access field keeps only that field instead of
// becoming the omission marker.
func TestBoundExecutedToolCallsShedsLargestSnapshotFirstLikeRust(t *testing.T) {
	newItem := func(name string, callID string, metadata any) *AgentItem {
		item := &AgentItem{Type: "function_call", Name: name, CallID: callID, Arguments: `{}`}
		RecordExecutedToolCall(item)
		calls := item.ExecutedToolCalls()
		calls[0].SetToolResultMetadata(NewToolResultMetadata(metadata))
		item.ReplaceExecutedToolCalls(calls)
		return item
	}
	resourceAccess := map[string]any{"openai/resource_access": map[string]any{"id": "res-1"}, "big": strings.Repeat("b", MaxExecutedToolCallMetadataBytes)}
	large := newItem("large", "call-large", resourceAccess)
	small := newItem("small", "call-small", map[string]any{"provider": "small"})

	bounded := BoundExecutedToolCallsForPrompt([]any{large, small})
	if executedToolCallMetadataBytes(bounded[0].(*AgentItem))+executedToolCallMetadataBytes(bounded[1].(*AgentItem)) > MaxExecutedToolCallMetadataBytes {
		t.Fatal("bounded metadata exceeds the prompt budget")
	}
	largeCalls := bounded[0].(*AgentItem).ExecutedToolCalls()
	if len(largeCalls) != 1 || !largeCalls[0].HasToolResultMetadata() {
		t.Fatalf("large call metadata = %#v", largeCalls)
	}
	retained, _ := largeCalls[0].toolResultMetadata.value.(map[string]any)
	if len(retained) != 1 || retained["openai/resource_access"] == nil {
		t.Fatalf("resource access was not retained alone: %#v", largeCalls[0].toolResultMetadata.value)
	}
	smallCalls := bounded[1].(*AgentItem).ExecutedToolCalls()
	smallRetained, _ := smallCalls[0].toolResultMetadata.value.(map[string]any)
	if len(smallRetained) != 1 || smallRetained["provider"] != "small" {
		t.Fatalf("small snapshot was shed: %#v", smallCalls[0].toolResultMetadata.value)
	}

	// Without the resource-access field the large snapshot becomes the marker,
	// and the smaller unrelated snapshot still survives.
	plain := newItem("large-plain", "call-plain", map[string]any{"provider": strings.Repeat("p", MaxExecutedToolCallMetadataBytes)})
	smallAgain := newItem("small-again", "call-small-again", map[string]any{"provider": "small"})
	bounded = BoundExecutedToolCallsForPrompt([]any{plain, smallAgain})
	plainCalls := bounded[0].(*AgentItem).ExecutedToolCalls()
	if !plainCalls[0].toolResultMetadata.isOmittedDueToSizeLimit() {
		t.Fatalf("plain snapshot was not omitted: %#v", plainCalls[0].toolResultMetadata.value)
	}
	kept, _ := bounded[1].(*AgentItem).ExecutedToolCalls()[0].toolResultMetadata.value.(map[string]any)
	if kept["provider"] != "small" {
		t.Fatalf("unrelated small snapshot was shed: %#v", kept)
	}
}

func TestToolResultMetadataDestinationFiltering(t *testing.T) {
	for _, allowed := range []string{
		"https://api.openai.com/v1",
		"https://chatgpt.com/backend-api/codex",
		"https://chat.openai.com",
		"https://chatgpt-staging.com",
		"https://staging.chatgpt.com",
	} {
		if !toolResultMetadataDestinationAllowed(allowed) {
			t.Fatalf("destination %q must allow raw metadata", allowed)
		}
	}
	for _, denied := range []string{
		"https://evilchatgpt.com",
		"https://api.openai.com.evil.example",
		"http://api.openai.com/v1",
		"https://example.com/v1",
		"",
	} {
		if toolResultMetadataDestinationAllowed(denied) {
			t.Fatalf("destination %q must not allow raw metadata", denied)
		}
	}

	serialized := map[string]any{
		internalChatMessageMetadataPassthroughField: map[string]any{
			executedToolCallsField: []any{
				map[string]any{"name": "tool", "tool_result_metadata": map[string]any{"k": "v"}},
			},
		},
	}
	clearToolResultMetadataInPromptItem(serialized)
	calls := serialized[internalChatMessageMetadataPassthroughField].(map[string]any)[executedToolCallsField].([]any)
	if _, present := calls[0].(map[string]any)[toolResultMetadataField]; present {
		t.Fatal("destination filter must strip serialized raw metadata")
	}
}

// Mirrors Rust #48344's `ModelProvider::include_internal_metadata`: a
// runtime-only provider grant lets the resolved provider receive internal tool
// metadata even when its endpoint would fail the destination check, and it never
// reaches serialized configuration.
func TestToolResultMetadataProviderGrantLikeRust(t *testing.T) {
	overridden := "https://proxy.example.test/v1"

	granted := &ResponsesAgentRunner{Provider: &APIProvider{BaseURL: overridden, IncludeInternalMetadata: true}}
	if items := granted.filterToolResultMetadataForDestination([]any{metadataPassthroughItem()}); !itemHasRawToolResultMetadata(items[0]) {
		t.Fatal("the provider grant must keep raw metadata at an overridden endpoint")
	}
	plain := &ResponsesAgentRunner{Provider: &APIProvider{BaseURL: overridden}}
	if items := plain.filterToolResultMetadataForDestination([]any{metadataPassthroughItem()}); itemHasRawToolResultMetadata(items[0]) {
		t.Fatal("a provider without the grant must strip raw metadata at an untrusted endpoint")
	}
	firstParty := &ResponsesAgentRunner{Provider: &APIProvider{BaseURL: "https://api.openai.com/v1"}}
	if items := firstParty.filterToolResultMetadataForDestination([]any{metadataPassthroughItem()}); !itemHasRawToolResultMetadata(items[0]) {
		t.Fatal("the destination check must keep raw metadata at a first-party endpoint")
	}

	// The built-in OpenAI provider carries the grant, so an overridden base URL
	// keeps sending metadata; a provider built from configuration does not.
	openAI := CreateOpenAIProvider(overridden)
	if !openAI.IncludeInternalMetadata {
		t.Fatal("the built-in OpenAI provider lost its grant")
	}
	resolved, err := openAI.ToAPIProvider("chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.IncludeInternalMetadata {
		t.Fatal("the resolved OpenAI provider lost the grant")
	}
	customInfo := ProviderInfo{Name: "custom", BaseURL: overridden}
	resolvedCustom, err := customInfo.ToAPIProvider("chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	if resolvedCustom.IncludeInternalMetadata {
		t.Fatal("a configured provider gained the runtime-only grant")
	}
	encoded, err := json.Marshal(openAI)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "include_internal_metadata") {
		t.Fatalf("the runtime-only grant was serialized: %s", encoded)
	}

	// A provider received through remote configuration keeps the grant false:
	// the field is invisible to the wire, so decoding cannot turn it on.
	received := ProviderInfo{}
	if err := json.Unmarshal([]byte(`{"name":"custom","base_url":"https://proxy.example.test/v1","include_internal_metadata":true}`), &received); err != nil {
		t.Fatal(err)
	}
	if received.IncludeInternalMetadata {
		t.Fatal("a provider received through configuration gained the runtime-only grant")
	}
}

// The Responses runner clones the resolved provider at construction, so a
// dropped grant would silently re-enable destination-only filtering for the
// built-in OpenAI provider's overridden endpoint (Rust #48344 applies the grant
// on both the HTTP and WebSocket request paths).
func TestToolResultMetadataGrantSurvivesRunnerConstruction(t *testing.T) {
	overridden := "https://proxy.example.test/v1"

	openAIInfo := CreateOpenAIProvider(overridden)
	openAIProvider, err := openAIInfo.ToAPIProvider("chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	runner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &openAIProvider})
	if !runner.Provider.IncludeInternalMetadata {
		t.Fatal("the runner clone dropped the built-in OpenAI provider's grant")
	}
	if items := runner.filterToolResultMetadataForDestination([]any{metadataPassthroughItem()}); !itemHasRawToolResultMetadata(items[0]) {
		t.Fatal("the built-in OpenAI provider must keep raw metadata at an overridden endpoint")
	}

	customInfo := ProviderInfo{Name: "custom", BaseURL: overridden}
	customProvider, err := customInfo.ToAPIProvider("")
	if err != nil {
		t.Fatal(err)
	}
	customRunner := NewResponsesAgentRunner(&ResponsesAgentOptions{Provider: &customProvider})
	if customRunner.Provider.IncludeInternalMetadata {
		t.Fatal("an ungranted configured provider must not gain the runtime-only grant")
	}
	if items := customRunner.filterToolResultMetadataForDestination([]any{metadataPassthroughItem()}); itemHasRawToolResultMetadata(items[0]) {
		t.Fatal("an ungranted provider must strip raw metadata at a custom endpoint")
	}
}

func metadataPassthroughItem() any {
	return map[string]any{
		internalChatMessageMetadataPassthroughField: map[string]any{
			executedToolCallsField: []any{
				map[string]any{"name": "tool", "tool_result_metadata": map[string]any{"k": "v"}},
			},
		},
	}
}

func itemHasRawToolResultMetadata(item any) bool {
	object, _ := item.(map[string]any)
	metadata, _ := object[internalChatMessageMetadataPassthroughField].(map[string]any)
	calls, _ := metadata[executedToolCallsField].([]any)
	if len(calls) == 0 {
		return false
	}
	call, _ := calls[0].(map[string]any)
	_, present := call[toolResultMetadataField]
	return present
}
