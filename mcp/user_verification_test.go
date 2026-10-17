package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeUserVerificationResponsePreservesOnlyClosedReasons(t *testing.T) {
	for _, reason := range []UserVerificationReason{
		UserVerificationReasonUserCancelled,
		UserVerificationReasonUserDeclined,
		UserVerificationReasonCredentialMissing,
		UserVerificationReasonBiometricsUnavailable,
		UserVerificationReasonProviderUnavailable,
		UserVerificationReasonAuthenticationFailed,
		UserVerificationReasonTimeout,
		UserVerificationReasonProviderError,
		UserVerificationReasonServiceError,
		UserVerificationReasonInvalidParams,
		UserVerificationReasonInvalidProof,
		UserVerificationReasonInvalidResponse,
		UserVerificationReasonResponseError,
		UserVerificationReasonResponseChannelClosed,
		UserVerificationReasonInterrupted,
		UserVerificationReasonApprovalUnavailable,
	} {
		response := NormalizeUserVerificationResponse(&MCPElicitationResponse{
			Action:  MCPElicitationActionCancel,
			Content: map[string]any{"signature": "must-not-leak"},
			Meta: map[string]any{
				UserVerificationReasonKey: string(reason),
				"unrelated":               "discard",
			},
		})
		wantMeta := map[string]any{UserVerificationReasonKey: string(reason)}
		if response.Action != MCPElicitationActionCancel || response.Content != nil || !reflect.DeepEqual(response.Meta, wantMeta) {
			t.Fatalf("reason %q response = %#v, want cancel with %#v", reason, response, wantMeta)
		}
	}

	for _, meta := range []any{
		nil,
		map[string]any{UserVerificationReasonKey: "private native failure"},
		map[string]any{"message": "private native failure"},
	} {
		response := NormalizeUserVerificationResponse(&MCPElicitationResponse{
			Action: MCPElicitationActionDecline,
			Meta:   meta,
		})
		if response.Action != MCPElicitationActionDecline || response.Meta != nil {
			t.Fatalf("unknown meta %#v response = %#v, want decline without meta", meta, response)
		}
	}
}

func TestNormalizeUserVerificationResponseValidatesProof(t *testing.T) {
	signature := base64.RawURLEncoding.EncodeToString([]byte("proof"))
	valid := &MCPElicitationResponse{
		Action: MCPElicitationActionAccept,
		Content: map[string]any{
			"credentialId": "credential-1",
			"signature":    signature,
		},
		Meta: map[string]any{"unrelated": "drop"},
	}
	normalized := NormalizeUserVerificationResponse(valid)
	if normalized.Action != MCPElicitationActionAccept || normalized.Meta != nil ||
		!reflect.DeepEqual(normalized.Content, valid.Content) {
		t.Fatalf("valid proof response = %#v", normalized)
	}

	for _, content := range []any{
		nil,
		map[string]any{"credentialId": "", "signature": signature},
		map[string]any{"credentialId": "credential-1", "signature": ""},
		map[string]any{"credentialId": "credential-1", "signature": "not base64!"},
		map[string]any{"credentialId": "credential-1"},
		map[string]any{"credentialId": "credential-1", "signature": signature, "extra": true},
	} {
		response := NormalizeUserVerificationResponse(&MCPElicitationResponse{
			Action:  MCPElicitationActionAccept,
			Content: content,
		})
		if response.Action != MCPElicitationActionCancel || response.Content != nil ||
			!reflect.DeepEqual(response.Meta, UserVerificationReasonMeta(UserVerificationReasonInvalidProof)) {
			t.Fatalf("invalid proof %#v response = %#v", content, response)
		}
	}

	invalidAction := NormalizeUserVerificationResponse(&MCPElicitationResponse{Action: "future-action"})
	if invalidAction.Action != MCPElicitationActionCancel || invalidAction.Content != nil ||
		!reflect.DeepEqual(invalidAction.Meta, UserVerificationReasonMeta(UserVerificationReasonInvalidResponse)) {
		t.Fatalf("invalid action response = %#v", invalidAction)
	}
}

func TestUserVerificationReasonMetaRoundTrip(t *testing.T) {
	for _, reason := range []UserVerificationReason{
		UserVerificationReasonUserCancelled,
		UserVerificationReasonInvalidProof,
		UserVerificationReasonApprovalUnavailable,
	} {
		meta := UserVerificationReasonMeta(reason)
		if got, ok := UserVerificationReasonFromMeta(meta); !ok || got != reason {
			t.Fatalf("UserVerificationReasonFromMeta(%#v) = %q, %v", meta, got, ok)
		}
	}
	if reason, ok := UserVerificationReasonFromMeta(map[string]any{UserVerificationReasonKey: 7}); ok || reason != "" {
		t.Fatalf("non-string reason = %q, %v", reason, ok)
	}
}

func TestMCPElicitationResultNormalizesUserVerificationLikeRust(t *testing.T) {
	params := []byte(`{"mode":"openai/userVerification","title":"Approve","description":"","challenge":"AQ"}`)
	result := mcpElicitationResult(context.Background(), "server", MCPElicitationHandlerFunc(
		func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return &MCPElicitationResponse{
				Action:  MCPElicitationActionAccept,
				Content: map[string]any{"credentialId": "credential", "signature": ""},
			}, nil
		},
	), "openai/userVerification", []byte("1"), params)
	response, ok := result.(*MCPElicitationResponse)
	if !ok || response.Action != MCPElicitationActionCancel ||
		!reflect.DeepEqual(response.Meta, UserVerificationReasonMeta(UserVerificationReasonInvalidProof)) {
		t.Fatalf("invalid proof result = %#v", result)
	}

	result = mcpElicitationResult(context.Background(), "server", MCPElicitationHandlerFunc(
		func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return &MCPElicitationResponse{
				Action: MCPElicitationActionDecline,
				Meta: map[string]any{
					UserVerificationReasonKey: string(UserVerificationReasonCredentialMissing),
					"message":                 "private provider diagnostic",
				},
			}, nil
		},
	), "openai/userVerification", []byte("2"), params)
	response, ok = result.(*MCPElicitationResponse)
	if !ok || response.Action != MCPElicitationActionDecline ||
		!reflect.DeepEqual(response.Meta, UserVerificationReasonMeta(UserVerificationReasonCredentialMissing)) {
		t.Fatalf("decline result = %#v", result)
	}

	result = mcpElicitationResult(context.Background(), "server", MCPElicitationHandlerFunc(
		func(context.Context, *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return nil, errors.New("private provider diagnostic")
		},
	), "openai/userVerification", []byte("3"), params)
	response, ok = result.(*MCPElicitationResponse)
	if !ok || response.Action != MCPElicitationActionCancel ||
		!reflect.DeepEqual(response.Meta, UserVerificationReasonMeta(UserVerificationReasonResponseError)) {
		t.Fatalf("handler error result = %#v", result)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	result = mcpElicitationResult(cancelled, "server", nil, "openai/userVerification", []byte("4"), params)
	response, ok = result.(*MCPElicitationResponse)
	if !ok || response.Action != MCPElicitationActionCancel ||
		!reflect.DeepEqual(response.Meta, UserVerificationReasonMeta(UserVerificationReasonInterrupted)) {
		t.Fatalf("cancelled result = %#v", result)
	}
}
