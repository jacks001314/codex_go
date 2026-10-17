package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/mcp"
	codextea "codex_go/tui/tea"
)

func TestUserVerificationElicitationResponsesCarryOnlyBoundedReasonsLikeRust(t *testing.T) {
	broker := &interactiveElicitationBroker{}
	cancelled := broker.elicitationResponse(codextea.ModalResponse{Cancelled: true}, false, true)
	assertMCPUserVerificationReason(t, cancelled.Action, cancelled.Content, cancelled.Meta, mcp.MCPElicitationActionCancel, mcp.UserVerificationReasonUserCancelled)

	declined := broker.elicitationResponse(codextea.ModalResponse{
		Elicitation: &codextea.ElicitationDecision{
			Action:  string(mcp.MCPElicitationActionDecline),
			Content: map[string]any{"proof": "must-not-leak"},
		},
	}, false, true)
	assertMCPUserVerificationReason(t, declined.Action, declined.Content, declined.Meta, mcp.MCPElicitationActionDecline, mcp.UserVerificationReasonUserDeclined)

	proof := map[string]any{"credentialId": "credential", "signature": "c2ln"}
	accepted := broker.elicitationResponse(codextea.ModalResponse{
		Elicitation: &codextea.ElicitationDecision{
			Action:  string(mcp.MCPElicitationActionAccept),
			Content: proof,
		},
	}, false, true)
	if accepted.Action != mcp.MCPElicitationActionAccept || accepted.Meta != nil || !reflect.DeepEqual(accepted.Content, proof) {
		t.Fatalf("accepted verification response = %#v", accepted)
	}

	remoteCancel := remoteMCPElicitationResponse(codextea.ModalResponse{Cancelled: true}, false, true)
	assertRemoteUserVerificationReason(t, remoteCancel.Action, remoteCancel.Content, remoteCancel.Meta, appserver.MCPElicitationActionCancel, mcp.UserVerificationReasonUserCancelled)
	remoteDecline := remoteMCPElicitationResponse(codextea.ModalResponse{
		Elicitation: &codextea.ElicitationDecision{
			Action:  string(appserver.MCPElicitationActionDecline),
			Content: map[string]any{"proof": "must-not-leak"},
		},
	}, false, true)
	assertRemoteUserVerificationReason(t, remoteDecline.Action, remoteDecline.Content, remoteDecline.Meta, appserver.MCPElicitationActionDecline, mcp.UserVerificationReasonUserDeclined)
	remoteAccept := remoteMCPElicitationResponse(codextea.ModalResponse{
		Elicitation: &codextea.ElicitationDecision{
			Action:  string(appserver.MCPElicitationActionAccept),
			Content: proof,
		},
	}, false, true)
	if remoteAccept.Action != appserver.MCPElicitationActionAccept || remoteAccept.Meta != nil || !reflect.DeepEqual(remoteAccept.Content, proof) {
		t.Fatalf("remote accepted verification response = %#v", remoteAccept)
	}

	if !remoteMCPElicitationUserVerification(&appserver.MCPElicitationRequestParams{Mode: "openai/userVerification"}) {
		t.Fatal("remote verification mode was not recognized")
	}

	local, err := broker.mcpElicitationFunc(func(bubbletea.Msg) {})(context.Background(), &mcp.MCPElicitationRequest{
		Method: "openai/userVerification",
		Mode:   "openai/userVerification",
	})
	if err != nil {
		t.Fatalf("local user verification error = %v", err)
	}
	assertMCPUserVerificationReason(t, local.Action, local.Content, local.Meta, mcp.MCPElicitationActionCancel, mcp.UserVerificationReasonProviderUnavailable)
}

func TestVerificationFailureMapsTypedReasonsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want mcp.UserVerificationReason
	}{
		{
			name: "invalid params",
			err:  &remoteRPCError{Data: map[string]any{"type": "invalidRequest", "reason": "invalidParams"}},
			want: mcp.UserVerificationReasonInvalidParams,
		},
		{
			name: "credential missing",
			err:  &remoteRPCError{Data: map[string]any{"type": "unavailable", "reason": "credentialMissing"}},
			want: mcp.UserVerificationReasonCredentialMissing,
		},
		{
			name: "biometrics unavailable",
			err:  &remoteRPCError{Data: map[string]any{"type": "unavailable", "reason": "biometricsUnavailable"}},
			want: mcp.UserVerificationReasonBiometricsUnavailable,
		},
		{
			name: "provider unavailable",
			err:  &remoteRPCError{Data: map[string]any{"type": "unavailable", "reason": "providerUnavailable"}},
			want: mcp.UserVerificationReasonProviderUnavailable,
		},
		{
			name: "cancelled",
			err:  &remoteRPCError{Data: map[string]any{"type": "cancelled", "reason": "userCancelled"}},
			want: mcp.UserVerificationReasonUserCancelled,
		},
		{
			name: "interrupted",
			err:  &remoteRPCError{Data: map[string]any{"type": "cancelled", "reason": "interrupted"}},
			want: mcp.UserVerificationReasonInterrupted,
		},
		{
			name: "authentication failed",
			err:  &remoteRPCError{Data: map[string]any{"type": "failed", "reason": "authenticationFailed"}},
			want: mcp.UserVerificationReasonAuthenticationFailed,
		},
		{
			name: "timeout",
			err:  &remoteRPCError{Data: map[string]any{"type": "failed", "reason": "timeout"}},
			want: mcp.UserVerificationReasonTimeout,
		},
		{
			name: "provider error",
			err:  &remoteRPCError{Data: map[string]any{"type": "failed", "reason": "providerError"}},
			want: mcp.UserVerificationReasonProviderError,
		},
		{
			name: "service error",
			err:  &remoteRPCError{Data: map[string]any{"type": "failed", "reason": "serviceError"}},
			want: mcp.UserVerificationReasonServiceError,
		},
		{
			name: "unknown reason",
			err:  &remoteRPCError{Data: map[string]any{"type": "unavailable", "reason": "private native failure"}},
			want: mcp.UserVerificationReasonServiceError,
		},
		{
			name: "transport error",
			err:  errors.New("connection reset"),
			want: mcp.UserVerificationReasonServiceError,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := userVerificationFailureReason(verificationFailure(testCase.err)); got != testCase.want {
				t.Fatalf("reason = %q, want %q", got, testCase.want)
			}
		})
	}
}

func assertMCPUserVerificationReason(t *testing.T, action mcp.MCPElicitationAction, content any, meta any, wantAction mcp.MCPElicitationAction, wantReason mcp.UserVerificationReason) {
	t.Helper()
	if action != wantAction || content != nil {
		t.Fatalf("response action/content = %q/%#v, want %q/nil", action, content, wantAction)
	}
	if reason, ok := mcp.UserVerificationReasonFromMeta(meta); !ok || reason != wantReason {
		t.Fatalf("verification reason = %q, %v; want %q", reason, ok, wantReason)
	}
}

func assertRemoteUserVerificationReason(t *testing.T, action appserver.MCPElicitationAction, content any, meta any, wantAction appserver.MCPElicitationAction, wantReason mcp.UserVerificationReason) {
	t.Helper()
	if action != wantAction || content != nil {
		t.Fatalf("response action/content = %q/%#v, want %q/nil", action, content, wantAction)
	}
	if reason, ok := mcp.UserVerificationReasonFromMeta(meta); !ok || reason != wantReason {
		t.Fatalf("verification reason = %q, %v; want %q", reason, ok, wantReason)
	}
}
