package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustToolResultBody(t *testing.T, source ToolResultBodySource) *ToolResultBody {
	t.Helper()
	body := ProjectToolResultBody(source)
	if body == nil {
		t.Fatal("ProjectToolResultBody returned nil")
	}
	return body
}

func TestProjectToolResultBodyProjectsTextAndStructuredContentLikeRust(t *testing.T) {
	body := mustToolResultBody(t, ToolResultBodySource{
		Content: []any{
			map[string]any{"type": "text", "text": "first"},
			map[string]any{"type": "text", "text": "second", "_meta": map[string]any{"other": true}},
		},
		StructuredContent: map[string]any{"answer": float64(42)},
	})
	want := `{"content":[{"type":"text","text":"first"},{"type":"text","text":"second"}],"structuredContent":{"answer":42}}`
	if body.JSON() != want {
		t.Fatalf("body = %s, want %s", body.JSON(), want)
	}
	if body.CompactBytes() != len(want) {
		t.Fatalf("CompactBytes = %d, want %d", body.CompactBytes(), len(want))
	}
	if !strings.Contains(body.String(), "redacted") {
		t.Fatalf("String() = %q, want a redacted form", body.String())
	}
}

func TestProjectToolResultBodyOmitsStructuredContentWhenAbsentLikeRust(t *testing.T) {
	body := mustToolResultBody(t, ToolResultBodySource{Content: []any{map[string]any{"type": "text", "text": "hi"}}})
	if body.JSON() != `{"content":[{"type":"text","text":"hi"}]}` {
		t.Fatalf("body = %s", body.JSON())
	}
}

func TestProjectToolResultBodyRejectsIneligibleResultsLikeRust(t *testing.T) {
	deep := map[string]any{}
	cursor := deep
	for depth := 0; depth < maxToolResultDepth+1; depth++ {
		child := map[string]any{}
		cursor["child"] = child
		cursor = child
	}
	cases := map[string]ToolResultBodySource{
		"error result":        {IsError: true, Content: []any{map[string]any{"type": "text", "text": "boom"}}},
		"non-text block":      {Content: []any{map[string]any{"type": "image", "data": "AAAA"}}},
		"encrypted content":   {Content: []any{map[string]any{"type": "text", "text": "secret", "_meta": map[string]any{"codex/encryptedContent": true}}}},
		"missing text":        {Content: []any{map[string]any{"type": "text"}}},
		"non-string text":     {Content: []any{map[string]any{"type": "text", "text": float64(1)}}},
		"oversized text":      {Content: []any{map[string]any{"type": "text", "text": strings.Repeat("x", MaxToolResultBodyBytes+1)}}},
		"non-object struct":   {Content: []any{map[string]any{"type": "text", "text": "ok"}}, StructuredContent: []any{float64(1)}},
		"over-deep structure": {Content: []any{map[string]any{"type": "text", "text": "ok"}}, StructuredContent: deep},
	}
	for name, source := range cases {
		if body := ProjectToolResultBody(source); body != nil {
			t.Fatalf("%s: ProjectToolResultBody = %s, want nil", name, body.JSON())
		}
	}
}

const toolResultsRequestJSON = `{"model":"gpt-5","input":[` +
	`{"type":"function_call_output","call_id":"c1","output":"ok","internal_chat_message_metadata_passthrough":{"executed_tool_calls":[` +
	`{"name":"first","arguments":{"a":1}},{"name":"second","arguments":{"b":2}}]}},` +
	`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"tools":[]}`

func TestWithToolResultsAttachesBodyToSelectedRowLikeRust(t *testing.T) {
	body := mustToolResultBody(t, ToolResultBodySource{Content: []any{map[string]any{"type": "text", "text": "result"}}})
	enriched, ok := WithToolResults(toolResultsRequestJSON, []ToolResultAttachment{{ItemIndex: 0, CallIndex: 1, Body: body}})
	if !ok {
		t.Fatal("WithToolResults reported no enrichment")
	}
	want := strings.Replace(toolResultsRequestJSON,
		`{"name":"second","arguments":{"b":2}}`,
		`{"name":"second","arguments":{"b":2},"tool_result":`+body.JSON()+`}`, 1)
	if enriched != want {
		t.Fatalf("enriched = %s, want %s", enriched, want)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(enriched), &decoded); err != nil {
		t.Fatalf("enriched body is not valid JSON: %v", err)
	}
	if len(decoded["input"].([]any)) != 2 {
		t.Fatalf("input items changed: %v", decoded["input"])
	}
}

func TestWithToolResultsRefusesUnresolvableRowsLikeRust(t *testing.T) {
	body := mustToolResultBody(t, ToolResultBodySource{Content: []any{map[string]any{"type": "text", "text": "result"}}})
	cases := map[string]struct {
		json        string
		attachments []ToolResultAttachment
	}{
		"no attachments":          {toolResultsRequestJSON, nil},
		"item index out of range": {toolResultsRequestJSON, []ToolResultAttachment{{ItemIndex: 3, CallIndex: 0, Body: body}}},
		"call index out of range": {toolResultsRequestJSON, []ToolResultAttachment{{ItemIndex: 0, CallIndex: 9, Body: body}}},
		"item without metadata":   {toolResultsRequestJSON, []ToolResultAttachment{{ItemIndex: 1, CallIndex: 0, Body: body}}},
		"nil body":                {toolResultsRequestJSON, []ToolResultAttachment{{ItemIndex: 0, CallIndex: 0}}},
		"no input field":          {`{"model":"gpt-5"}`, []ToolResultAttachment{{ItemIndex: 0, CallIndex: 0, Body: body}}},
	}
	for name, tc := range cases {
		if enriched, ok := WithToolResults(tc.json, tc.attachments); ok {
			t.Fatalf("%s: WithToolResults = %s, want no enrichment", name, enriched)
		}
	}
}

func TestWithToolResultsSkipsBodiesBeyondMessageBudgetLikeRust(t *testing.T) {
	body := mustToolResultBody(t, ToolResultBodySource{Content: []any{map[string]any{"type": "text", "text": "result"}}})
	remaining := MaxResponseMessageBytes - len(toolResultsRequestJSON)
	oversized := &ToolResultBody{json: `"` + strings.Repeat("x", remaining) + `"`}
	if enriched, ok := WithToolResults(toolResultsRequestJSON, []ToolResultAttachment{{ItemIndex: 0, CallIndex: 0, Body: oversized}}); ok {
		t.Fatalf("WithToolResults = %s, want no enrichment beyond the message budget", enriched)
	}
	enriched, ok := WithToolResults(toolResultsRequestJSON, []ToolResultAttachment{
		{ItemIndex: 0, CallIndex: 0, Body: oversized},
		{ItemIndex: 0, CallIndex: 1, Body: body},
	})
	if !ok || !strings.Contains(enriched, `"name":"second","arguments":{"b":2},"tool_result":`) {
		t.Fatalf("second attachment was not kept: %q", enriched)
	}
	if strings.Contains(enriched, `"name":"first","arguments":{"a":1},"tool_result":`) {
		t.Fatalf("over-budget attachment was kept: %q", enriched)
	}
}

func TestToolResultMetadataKeepsBodyBindingWhileSheddingLikeRust(t *testing.T) {
	metadata := NewToolResultMetadata(map[string]any{"openai/resource_access": map[string]any{"id": "R1"}}).WithResultID("result-1")
	if !metadata.HasObservation() || metadata.ResultID() != "result-1" {
		t.Fatalf("binding not recorded: %#v", metadata)
	}
	shrunk := metadata
	if !shrunk.retainResourceAccess() {
		t.Fatal("retainResourceAccess reported no change")
	}
	if shrunk.ResultID() != "result-1" {
		t.Fatalf("retainResourceAccess dropped the binding: %q", shrunk.ResultID())
	}
	shrunk.clearValue()
	if shrunk.IsSome() || shrunk.ResultID() != "result-1" || !shrunk.HasObservation() {
		t.Fatalf("clearValue = %#v, want a body-only observation", shrunk)
	}
	encoded, err := json.Marshal(shrunk)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	if string(encoded) != "null" {
		t.Fatalf("serialized binding = %s, want null (the id stays local)", encoded)
	}

	call := NewExecutedToolCall("tool", map[string]any{"q": "x"})
	call.SetToolResultMetadata(NewToolResultMetadata(map[string]any{"k": "v"}).WithResultID("result-2"))
	call.ClearToolResultMetadata()
	if call.ToolResultID() != "result-2" || call.HasToolResultMetadata() != true {
		t.Fatalf("ClearToolResultMetadata dropped the binding: %#v", call)
	}
	call.ClearToolResultID()
	if call.ToolResultID() != "" || call.HasToolResultMetadata() {
		t.Fatalf("ClearToolResultID kept an observation: %#v", call)
	}

	bodyOnly := NewExecutedToolCall("tool", nil)
	bodyOnly.SetToolResultMetadata(ToolResultMetadata{}.WithResultID("result-3"))
	if !bodyOnly.HasToolResultMetadata() {
		t.Fatal("a body-only observation was not recorded")
	}
	encoded, err = json.Marshal(bodyOnly)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}
	if strings.Contains(string(encoded), "result-3") {
		t.Fatalf("serialized call leaked the local result id: %s", encoded)
	}
}
