package appserver

import (
	"context"
	"testing"

	"codex_go/apps"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/tool"
)

// newMCPToolApprovalProvenanceHandler wires a runtime whose elicitation answers
// come from the supplied resolver, mirroring Rust #54679's app-server path.
func newMCPToolApprovalProvenanceHandler(t *testing.T, resolve func(*ServerRequest) *Response) *appserverMCPToolApprovalHandler {
	t.Helper()
	router := NewRuntimeRouter(RuntimeServices{})
	t.Cleanup(func() { _ = router.Close() })
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestMCPElicitation {
			t.Errorf("unexpected server request %s", request.Method)
			return
		}
		if response := resolve(request); response != nil {
			_, _ = router.requireServerRequests().Resolve(response)
		}
	}))
	return &appserverMCPToolApprovalHandler{
		router:                    router,
		responder:                 func(context.Context, string, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) { return nil, nil },
		threadID:                  "thread-1",
		turnID:                    "turn-1",
		approvalPolicy:            sandbox.ApprovalOnRequest,
		persistentApprovalAllowed: true,
		elicitationEnabled:        true,
		allowUserInteraction:      true,
	}
}

func mcpToolApprovalProvenanceRequest() *mcp.MCPToolApprovalRequest {
	return &mcp.MCPToolApprovalRequest{
		Server:       "docs",
		Tool:         "search",
		ApprovalMode: apps.AppToolApprovalAuto,
		CallID:       "call-1",
	}
}

// TestAppserverMCPToolApprovalCarriesCancellationReasonLikeRust mirrors Rust
// #54679's app-server half: an interrupted request reports itself as an
// interruption, a client cancel carries the provenance it sent, and both an
// absent reason and every non-abort decision keep the previous behavior.
func TestAppserverMCPToolApprovalCarriesCancellationReasonLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		response     *MCPElicitationRequestResponse
		resolveErr   bool
		wantDecision mcp.MCPToolApprovalDecision
		wantReason   mcp.MCPApprovalCancellationReason
	}{
		{
			name:         "interrupted request",
			resolveErr:   true,
			wantDecision: mcp.MCPToolApprovalDeny,
			wantReason:   mcp.MCPApprovalCancellationInterrupted,
		},
		{
			name:         "client cancellation with provenance",
			response:     &MCPElicitationRequestResponse{Action: MCPElicitationActionCancel, Meta: map[string]any{mcp.MCPApprovalCancellationReasonKey: "interrupted"}},
			wantDecision: mcp.MCPToolApprovalDeny,
			wantReason:   mcp.MCPApprovalCancellationInterrupted,
		},
		{
			name:         "client cancellation without provenance",
			response:     &MCPElicitationRequestResponse{Action: MCPElicitationActionCancel},
			wantDecision: mcp.MCPToolApprovalDeny,
			wantReason:   "",
		},
		{
			name:         "exec non-interactive provenance",
			response:     &MCPElicitationRequestResponse{Action: MCPElicitationActionCancel, Meta: map[string]any{mcp.MCPApprovalCancellationReasonKey: "non_interactive"}},
			wantDecision: mcp.MCPToolApprovalDeny,
			wantReason:   mcp.MCPApprovalCancellationNonInteractive,
		},
		{
			name:         "a decline keeps no provenance",
			response:     &MCPElicitationRequestResponse{Action: MCPElicitationActionDecline, Meta: map[string]any{mcp.MCPApprovalCancellationReasonKey: "interrupted"}},
			wantDecision: mcp.MCPToolApprovalReject,
			wantReason:   "",
		},
		{
			name:         "an approval keeps no provenance",
			response:     &MCPElicitationRequestResponse{Action: MCPElicitationActionAccept, Meta: map[string]any{mcp.MCPApprovalCancellationReasonKey: "interrupted"}},
			wantDecision: mcp.MCPToolApprovalApprove,
			wantReason:   "",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler := newMCPToolApprovalProvenanceHandler(t, func(request *ServerRequest) *Response {
				if testCase.resolveErr {
					return ErrorResponse(request.ID, -32603, "response channel closed", nil)
				}
				return OK(request.ID, testCase.response)
			})
			outcome, err := handler.ApproveMCPToolCall(context.Background(), mcpToolApprovalProvenanceRequest())
			if err != nil {
				t.Fatalf("ApproveMCPToolCall() error = %v", err)
			}
			if outcome.Decision != testCase.wantDecision {
				t.Fatalf("decision = %v, want %v", outcome.Decision, testCase.wantDecision)
			}
			if outcome.CancellationReason != testCase.wantReason {
				t.Fatalf("reason = %q, want %q", outcome.CancellationReason, testCase.wantReason)
			}
		})
	}
}

// TestAppserverMCPElicitationSynthesizedCancelCarriesInterruptionLikeRust
// mirrors the app-server half of #54679: the cancel synthesized for an
// interrupted tool-call approval records the interruption, while a synthesized
// cancel for any other elicitation keeps its metadata absent.
func TestAppserverMCPElicitationSynthesizedCancelCarriesInterruptionLikeRust(t *testing.T) {
	// A request the client never answers produces the synthesized cancel: the
	// handler's request context ends while the request is pending.
	handle := func(t *testing.T, request *mcp.MCPElicitationRequest) *mcp.MCPElicitationResponse {
		t.Helper()
		broker := NewServerRequestBroker()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		broker.SetSink(ServerRequestSinkFunc(func(*ServerRequest) {
			cancel()
		}))
		response, err := (&appserverMCPElicitationHandler{broker: broker}).HandleMCPElicitation(ctx, request)
		if err != nil {
			t.Fatalf("HandleMCPElicitation() error = %v", err)
		}
		return response
	}

	approval := handle(t, &mcp.MCPElicitationRequest{
		ServerName: "docs",
		Method:     "elicitation/create",
		Meta:       map[string]any{"codex_approval_kind": "mcp_tool_call"},
	})
	if approval == nil || approval.Action != mcp.MCPElicitationActionCancel {
		t.Fatalf("approval response = %#v", approval)
	}
	reason, ok := mcp.ParseMCPApprovalCancellationReason(approval.Meta)
	if !ok || reason != mcp.MCPApprovalCancellationInterrupted {
		t.Fatalf("approval cancellation reason = %#v (meta=%#v)", reason, approval.Meta)
	}

	other := handle(t, &mcp.MCPElicitationRequest{
		ServerName: "docs",
		Method:     "elicitation/create",
	})
	if other == nil || other.Action != mcp.MCPElicitationActionCancel || other.Meta != nil {
		t.Fatalf("non-approval response = %#v", other)
	}
}

// TestAppserverMCPToolApprovalUserInputProvenanceLikeRust covers the
// request_user_input transport of #54679: choosing cancel is a user action and a
// missing or failed response is an interruption.
func TestAppserverMCPToolApprovalUserInputProvenanceLikeRust(t *testing.T) {
	const questionID = mcp.MCPToolApprovalQuestionIDPrefix + "_call-1"
	for _, testCase := range []struct {
		name       string
		responder  tool.UserInputCallIDResponder
		wantDeny   bool
		wantReason mcp.MCPApprovalCancellationReason
	}{
		{
			name: "cancel answer is a user action",
			responder: func(context.Context, string, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
				return &tool.UserInputResponse{Answers: map[string]string{questionID: mcp.MCPToolApprovalCancel}}, nil
			},
			wantDeny:   true,
			wantReason: mcp.MCPApprovalCancellationUser,
		},
		{
			name: "missing response is an interruption",
			responder: func(context.Context, string, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
				return nil, nil
			},
			wantDeny:   true,
			wantReason: mcp.MCPApprovalCancellationInterrupted,
		},
		{
			name: "approval keeps no provenance",
			responder: func(context.Context, string, *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
				return &tool.UserInputResponse{Answers: map[string]string{questionID: mcp.MCPToolApprovalAccept}}, nil
			},
			wantReason: "",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			handler := newMCPToolApprovalProvenanceHandler(t, func(*ServerRequest) *Response { return nil })
			handler.elicitationEnabled = false
			handler.responder = testCase.responder
			outcome, err := handler.ApproveMCPToolCall(context.Background(), mcpToolApprovalProvenanceRequest())
			if err != nil {
				t.Fatalf("ApproveMCPToolCall() error = %v", err)
			}
			wantDecision := mcp.MCPToolApprovalApprove
			if testCase.wantDeny {
				wantDecision = mcp.MCPToolApprovalDeny
			}
			if outcome.Decision != wantDecision {
				t.Fatalf("decision = %v, want %v", outcome.Decision, wantDecision)
			}
			if outcome.CancellationReason != testCase.wantReason {
				t.Fatalf("reason = %q, want %q", outcome.CancellationReason, testCase.wantReason)
			}
		})
	}
}
