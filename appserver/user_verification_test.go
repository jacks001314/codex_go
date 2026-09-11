package appserver

import (
	"encoding/base64"
	"testing"
)

func TestRuntimeRouterUserVerificationUnavailableSurface(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})

	status := router.Handle(requestWithParams(t, IntID(1), MethodUserVerificationStatus, UserVerificationStatusParams{}))
	if status.Error != nil {
		t.Fatalf("userVerification/status error: %+v", status.Error)
	}
	statusResponse, ok := status.Result.(*UserVerificationStatusResponse)
	if !ok {
		t.Fatalf("status result = %#v", status.Result)
	}
	if statusResponse.CredentialID != nil {
		t.Fatalf("credentialId = %v, want nil", *statusResponse.CredentialID)
	}
	if statusResponse.UnavailableReason == nil || *statusResponse.UnavailableReason != UserVerificationUnavailableProviderUnavailable {
		t.Fatalf("unavailableReason = %v", statusResponse.UnavailableReason)
	}
	if statusResponse.UnavailableMessage == nil || *statusResponse.UnavailableMessage != userVerificationUnavailableMessage {
		t.Fatalf("unavailableMessage = %v", statusResponse.UnavailableMessage)
	}

	// Enroll/delete are unavailable, and cancellation is a no-op ack.
	enroll := router.Handle(requestWithParams(t, IntID(2), MethodUserVerificationEnroll, UserVerificationEnrollParams{}))
	assertUserVerificationUnavailable(t, enroll)
	deleteResponse := router.Handle(requestWithParams(t, IntID(3), MethodUserVerificationDelete, UserVerificationDeleteParams{}))
	assertUserVerificationUnavailable(t, deleteResponse)

	cancel := router.Handle(requestWithParams(t, IntID(4), MethodUserVerificationCancel, UserVerificationCancelParams{RequestID: IntID(99)}))
	if cancel.Error != nil {
		t.Fatalf("userVerification/cancel error: %+v", cancel.Error)
	}
	if _, ok := cancel.Result.(*UserVerificationCancelResponse); !ok {
		t.Fatalf("cancel result = %#v", cancel.Result)
	}
}

func TestRuntimeRouterUserVerificationVerifyValidation(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	validChallenge := base64.RawURLEncoding.EncodeToString([]byte("challenge-bytes"))

	invalid := router.Handle(requestWithParams(t, IntID(1), MethodUserVerificationVerify, UserVerificationVerifyParams{
		Challenge: "not-base64!!",
		Title:     "Approve",
	}))
	if invalid.Error == nil || invalid.Error.Code != JSONRPCInvalidParamsErrorCode || invalid.Error.Message != userVerificationInvalidMessage {
		t.Fatalf("invalid verify error = %+v", invalid.Error)
	}
	if got := invalid.Error.Data["type"]; got != "invalidRequest" {
		t.Fatalf("invalid verify data type = %v", got)
	}
	if got := invalid.Error.Data["reason"]; got != "invalidParams" {
		t.Fatalf("invalid verify data reason = %v", got)
	}

	emptyTitle := router.Handle(requestWithParams(t, IntID(2), MethodUserVerificationVerify, UserVerificationVerifyParams{
		Challenge: validChallenge,
		Title:     "",
	}))
	if emptyTitle.Error == nil || emptyTitle.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("empty title verify error = %+v", emptyTitle.Error)
	}

	// A well-formed request reaches the provider, which is unavailable in Go.
	valid := router.Handle(requestWithParams(t, IntID(3), MethodUserVerificationVerify, UserVerificationVerifyParams{
		Challenge:   validChallenge,
		Title:       "Approve",
		Description: "Confirm this action",
	}))
	assertUserVerificationUnavailable(t, valid)
}

func TestUserVerificationMethodsRequireExperimentalAPICapability(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	router.clientInfoMu.Lock()
	router.experimentalAPI[defaultRequestConnectionID] = false
	router.clientInfoMu.Unlock()

	response := router.Handle(requestWithParams(t, IntID(1), MethodUserVerificationStatus, UserVerificationStatusParams{}))
	if response.Error == nil || response.Error.Code != JSONRPCInvalidRequestErrorCode ||
		response.Error.Message != "userVerification/status requires experimentalApi capability" {
		t.Fatalf("gated status error = %+v", response.Error)
	}
}

func assertUserVerificationUnavailable(t *testing.T, response *Response) {
	t.Helper()
	if response.Error == nil {
		t.Fatalf("expected unavailable error, got result %#v", response.Result)
	}
	if response.Error.Code != JSONRPCInternalErrorCode || response.Error.Message != userVerificationUnavailableMessage {
		t.Fatalf("unavailable error = %+v", response.Error)
	}
	if got := response.Error.Data["type"]; got != "unavailable" {
		t.Fatalf("unavailable data type = %v", got)
	}
	if got := response.Error.Data["reason"]; got != "providerUnavailable" {
		t.Fatalf("unavailable data reason = %v", got)
	}
}
