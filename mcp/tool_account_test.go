package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/sandbox"
	"codex_go/tool"
)

// Mirrors Rust #42054's account resolution: required selectors must be valid
// argument strings, while legacy Apps tools keep their catalog identity.
func TestResolveMCPToolAccountLikeRust(t *testing.T) {
	required := &MCPToolInfo{Meta: map[string]any{
		"_codex_apps": map[string]any{"requires_explicit_link_id": true},
	}}
	legacy := &MCPToolInfo{Meta: map[string]any{
		"_codex_apps": map[string]any{"link_id": "catalog-link"},
	}}
	tests := []struct {
		name      string
		toolInfo  *MCPToolInfo
		arguments any
		want      string
		wantErr   bool
	}{
		{name: "required argument", toolInfo: required, arguments: map[string]any{"link_id": " argument-link "}, want: "argument-link"},
		{name: "required missing", toolInfo: required, arguments: map[string]any{}, wantErr: true},
		{name: "required null", toolInfo: required, arguments: map[string]any{"link_id": nil}, wantErr: true},
		{name: "required non-string", toolInfo: required, arguments: map[string]any{"link_id": 42}, wantErr: true},
		{name: "required blank", toolInfo: required, arguments: map[string]any{"link_id": "   "}, wantErr: true},
		{name: "required malformed arguments", toolInfo: required, arguments: "not-json", wantErr: true},
		{name: "legacy catalog link", toolInfo: legacy, arguments: map[string]any{}, want: "catalog-link"},
		{name: "legacy ignores argument link", toolInfo: legacy, arguments: map[string]any{"link_id": "argument-link"}, want: "catalog-link"},
		{name: "non-apps empty", toolInfo: &MCPToolInfo{}, arguments: map[string]any{}, want: ""},
		{name: "nil tool", toolInfo: nil, arguments: map[string]any{}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveMCPToolAccount(tc.toolInfo, tc.arguments)
			if tc.wantErr {
				var accountErr MCPToolAccountError
				if !errors.As(err, &accountErr) {
					t.Fatalf("error = %#v, want MCPToolAccountError", err)
				}
				if accountErr.Error() != "This app tool requires a non-empty string link_id argument" {
					t.Fatalf("error = %q", accountErr.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveMCPToolAccount() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("link id = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyMCPAppOutputMetadataPrefersResolvedArgumentLinkIDLikeRust(t *testing.T) {
	data := map[string]any{}
	ApplyMCPAppOutputMetadata(
		data,
		RuntimeCodexAppsMCPServerName,
		map[string]any{"_codex_apps": map[string]any{"link_id": "catalog-link"}},
		"calendar",
		"Calendar",
		"",
		"argument-link",
	)
	if data["link_id"] != "argument-link" {
		t.Fatalf("link_id = %#v, want argument-link", data["link_id"])
	}
}

func TestMCPToolExecutorRejectsMissingExplicitLinkIDBeforeApprovalLikeRust(t *testing.T) {
	approvalCalls := 0
	executor := NewToolExecutor(&ToolExecutorOptions{
		Service:    NewMCPService(nil),
		ServerName: RuntimeCodexAppsMCPServerName,
		ToolName:   tool.NamespacedName(RuntimeCodexAppsMCPServerName, "create_event"),
		ToolInfo: &MCPToolInfo{
			Name: "create_event",
			Meta: map[string]any{"_codex_apps": map[string]any{"requires_explicit_link_id": true}},
		},
		ToolApproval: &ToolApprovalOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			Handler: MCPToolApprovalHandlerFunc(func(context.Context, *MCPToolApprovalRequest) (MCPToolApprovalOutcome, error) {
				approvalCalls++
				return MCPToolApprovalOutcome{Decision: MCPToolApprovalApprove}, nil
			}),
		},
	})
	_, err := executor.Execute(context.Background(), &tool.Invocation{
		CallID:   "call-missing-link",
		ToolName: tool.NamespacedName(RuntimeCodexAppsMCPServerName, "create_event"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{}`},
	})
	var callErr *tool.FunctionCallError
	if !errors.As(err, &callErr) || !callErr.RespondsToModel() ||
		!strings.Contains(callErr.ModelMessage(), "requires a non-empty string link_id argument") {
		t.Fatalf("Execute() error = %#v", err)
	}
	if approvalCalls != 0 {
		t.Fatalf("approval calls = %d, want 0", approvalCalls)
	}
}
