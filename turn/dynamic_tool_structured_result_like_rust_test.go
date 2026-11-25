package turn

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"codex_go/tool"
)

func TestDynamicToolStructuredResultReachesCodeModeLikeRust(t *testing.T) {
	structured := map[string]any{"answer": float64(42), "nested": map[string]any{"ok": true}}
	caller := &fakeDynamicToolCaller{result: &DynamicToolCallResponse{
		StructuredContent: structured,
		Success:           true,
	}}
	executor := &dynamicToolExecutor{
		caller:   caller,
		threadID: "thread-1",
		turnID:   "turn-1",
		tool:     "lookup",
	}
	output, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:  "dynamic-1",
		Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"id":1}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if caller.params == nil || caller.params.SupportedResultFormat == nil ||
		*caller.params.SupportedResultFormat != DynamicToolResultFormatStructuredContent {
		t.Fatalf("supported result format = %#v", caller.params)
	}
	if !output.Success || output.ResultBodySource == nil {
		t.Fatalf("output = %#v", output)
	}
	if !reflect.DeepEqual(output.ResultBodySource.StructuredContent, structured) {
		t.Fatalf("structured content = %#v, want %#v", output.ResultBodySource.StructuredContent, structured)
	}
	if output.ResultBodySource.IsError {
		t.Fatal("successful structured result was marked as an error")
	}
	if output.Body != `{"answer":42,"nested":{"ok":true}}` {
		t.Fatalf("model body = %q", output.Body)
	}
}

func TestDynamicToolResultUnionValidationLikeRust(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
		check   func(*testing.T, DynamicToolCallResponse)
	}{
		{
			name:  "content items",
			value: `{"contentItems":[{"type":"inputText","text":"done"}],"success":true}`,
			check: func(t *testing.T, response DynamicToolCallResponse) {
				if response.resultKind != dynamicToolResultContentItems || len(response.ContentItems) != 1 {
					t.Fatalf("response = %#v", response)
				}
			},
		},
		{
			name:  "structured content",
			value: `{"structuredContent":{"done":true},"success":true}`,
			check: func(t *testing.T, response DynamicToolCallResponse) {
				if response.resultKind != dynamicToolResultStructuredContent || response.StructuredContent == nil {
					t.Fatalf("response = %#v", response)
				}
			},
		},
		{
			name:  "error",
			value: `{"errorMessage":"denied","success":false}`,
			check: func(t *testing.T, response DynamicToolCallResponse) {
				if response.resultKind != dynamicToolResultError || response.ErrorMessage != "denied" {
					t.Fatalf("response = %#v", response)
				}
			},
		},
		{name: "missing result", value: `{"success":true}`, wantErr: "expected exactly one"},
		{name: "multiple results", value: `{"contentItems":[],"structuredContent":{},"success":true}`, wantErr: "expected exactly one"},
		{name: "successful error", value: `{"errorMessage":"bad","success":true}`, wantErr: "requires success: false"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var response DynamicToolCallResponse
			err := json.Unmarshal([]byte(test.value), &response)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Unmarshal() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			test.check(t, response)
		})
	}
}
