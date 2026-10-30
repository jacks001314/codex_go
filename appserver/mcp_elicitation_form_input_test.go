package appserver

import (
	"context"
	"testing"

	"codex_go/mcp"
	"codex_go/sandbox"
)

func TestAppserverMCPElicitationFullAccessFormInputSurfacedLikeRust(t *testing.T) {
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestMCPElicitation {
			t.Fatalf("method = %s", request.Method)
		}
		_, _ = broker.Resolve(OK(request.ID, &MCPElicitationRequestResponse{
			Action:  MCPElicitationActionAccept,
			Content: map[string]any{"email": "user@example.com"},
		}))
	}))
	profile := sandbox.FullAccessPermissionProfile()
	handler := &appserverMCPElicitationHandler{
		broker: broker,
		authority: func(string, string, string) mcpElicitationAuthority {
			return mcpElicitationAuthority{
				ApprovalPolicy:    sandbox.ApprovalNever,
				ApprovalsReviewer: "user",
				PermissionProfile: &profile,
			}
		},
	}
	handler.EnableFullAccessFormInput()
	response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName:      "docs",
		ThreadID:        "thread-1",
		Method:          "elicitation/create",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"email": map[string]any{"type": "string"}}},
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v, want accepted (form surfaced to the client)", response)
	}
}

func TestAppserverMCPElicitationFullAccessFormInputDeclinedWithoutCapability(t *testing.T) {
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		t.Fatalf("client without the standard-form-input capability must not receive the form: %#v", request)
	}))
	profile := sandbox.FullAccessPermissionProfile()
	handler := &appserverMCPElicitationHandler{
		broker: broker,
		authority: func(string, string, string) mcpElicitationAuthority {
			return mcpElicitationAuthority{
				ApprovalPolicy:    sandbox.ApprovalNever,
				ApprovalsReviewer: "user",
				PermissionProfile: &profile,
			}
		},
	}
	response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName:      "docs",
		Method:          "elicitation/create",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"email": map[string]any{"type": "string"}}},
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionDecline {
		t.Fatalf("response = %#v, want decline", response)
	}
}

func TestAppserverMCPElicitationToolSuggestionAlwaysDeclined(t *testing.T) {
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		t.Fatalf("tool-suggestion elicitation must not be forwarded: %#v", request)
	}))
	profile := sandbox.FullAccessPermissionProfile()
	handler := &appserverMCPElicitationHandler{
		broker: broker,
		authority: func(string, string, string) mcpElicitationAuthority {
			return mcpElicitationAuthority{
				ApprovalPolicy:    sandbox.ApprovalNever,
				ApprovalsReviewer: "user",
				PermissionProfile: &profile,
			}
		},
	}
	handler.EnableFullAccessFormInput()
	response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName:      "docs",
		Method:          "elicitation/create",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{"email": map[string]any{"type": "string"}}},
		Meta:            map[string]any{"codex_approval_kind": "tool_suggestion"},
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionDecline {
		t.Fatalf("response = %#v, want decline", response)
	}
}

func TestAppserverMCPElicitationEmptyFormStillAutoAcceptedInFullAccess(t *testing.T) {
	broker := NewServerRequestBroker()
	broker.SetSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		t.Fatalf("empty form must be auto-accepted without forwarding: %#v", request)
	}))
	profile := sandbox.FullAccessPermissionProfile()
	handler := &appserverMCPElicitationHandler{
		broker: broker,
		authority: func(string, string, string) mcpElicitationAuthority {
			return mcpElicitationAuthority{
				ApprovalPolicy:    sandbox.ApprovalNever,
				ApprovalsReviewer: "user",
				PermissionProfile: &profile,
			}
		},
	}
	handler.EnableFullAccessFormInput()
	response, err := handler.HandleMCPElicitation(context.Background(), &mcp.MCPElicitationRequest{
		ServerName:      "docs",
		Method:          "elicitation/create",
		RequestedSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	})
	if err != nil {
		t.Fatalf("HandleMCPElicitation() error = %v", err)
	}
	if response == nil || response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v, want accept", response)
	}
}

func TestInitializeCapabilitiesStandardFormInputWireShape(t *testing.T) {
	params := &InitializeCapabilities{MCPServerStandardFormInput: true}
	if !params.MCPServerStandardFormInput {
		t.Fatal("MCPServerStandardFormInput not set")
	}
}
