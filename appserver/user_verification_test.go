package appserver

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
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

// Mirrors Rust's
// user_verification_cancel_stdio_validates_and_acknowledges_unknown_requests:
// the cancel params require exactly a non-null requestId, and an integer or a
// string request id is acknowledged even when unknown.
func TestRuntimeRouterUserVerificationCancelValidatesParams(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	for _, params := range []string{
		`{}`,
		`{"requestId":null}`,
		`{"requestId":1.5}`,
		`{"requestId":1,"connectionId":2}`,
	} {
		response := router.Handle(requestWithRawParams(t, IntID(1), MethodUserVerificationCancel, params))
		if response.Error == nil {
			t.Fatalf("cancel params %s were accepted: %#v", params, response.Result)
		}
	}
	for _, params := range []string{`{"requestId":100}`, `{"requestId":"100"}`} {
		response := router.Handle(requestWithRawParams(t, IntID(2), MethodUserVerificationCancel, params))
		if response.Error != nil {
			t.Fatalf("cancel params %s error = %+v", params, response.Error)
		}
		if _, ok := response.Result.(*UserVerificationCancelResponse); !ok {
			t.Fatalf("cancel params %s result = %#v", params, response.Result)
		}
	}
}

// Every user-verification params struct denies unknown fields like Rust's
// `#[serde(deny_unknown_fields)]`, verify requires all three fields, and an
// explicit null params object is invalid.
func TestRuntimeRouterUserVerificationParamsRejectUnknownFields(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	for _, testCase := range []struct {
		method Method
		params string
	}{
		{MethodUserVerificationStatus, `{"extra":1}`},
		{MethodUserVerificationEnroll, `{"extra":1}`},
		{MethodUserVerificationDelete, `{"extra":1}`},
		{MethodUserVerificationVerify, `{"challenge":"AQ","title":"Approve","description":"","extra":1}`},
		{MethodUserVerificationVerify, `{"challenge":"AQ","title":"Approve"}`},
		{MethodUserVerificationStatus, `null`},
	} {
		response := router.Handle(requestWithRawParams(t, IntID(1), testCase.method, testCase.params))
		if response.Error == nil {
			t.Fatalf("%s params %s were accepted: %#v", testCase.method, testCase.params, response.Result)
		}
	}
	// The empty params object still decodes: status answers locally, and the
	// mutating methods reach the unavailable provider instead of failing on the
	// params shape.
	if response := router.Handle(requestWithRawParams(t, IntID(2), MethodUserVerificationStatus, `{}`)); response.Error != nil {
		t.Fatalf("status empty params error = %+v", response.Error)
	}
	for _, method := range []Method{MethodUserVerificationEnroll, MethodUserVerificationDelete} {
		response := router.Handle(requestWithRawParams(t, IntID(3), method, `{}`))
		if response.Error == nil || response.Error.Code != JSONRPCInternalErrorCode {
			t.Fatalf("%s empty params error = %+v", method, response.Error)
		}
	}
}

func requestWithRawParams(t *testing.T, id RequestID, method Method, params string) *Request {
	t.Helper()
	if !json.Valid([]byte(params)) {
		t.Fatalf("invalid test params: %s", params)
	}
	return &Request{JSONRPC: "2.0", ID: id, Method: method, Params: json.RawMessage(params)}
}

// A network peer may only read the verification status: enroll, delete, and
// verify report providerUnavailable on a websocket connection, and the gate runs
// before validation, so a malformed challenge still reports it (Rust's
// ConnectionOrigin::WebSocket arm). A local transport keeps the validation order.
func TestRuntimeRouterUserVerificationPeerOriginLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	router.SetRequestTransport("websocket")

	status := router.Handle(requestWithParams(t, IntID(1), MethodUserVerificationStatus, UserVerificationStatusParams{}))
	if status.Error != nil {
		t.Fatalf("websocket status error = %+v", status.Error)
	}
	if response, ok := status.Result.(*UserVerificationStatusResponse); !ok || response.UnavailableReason == nil ||
		*response.UnavailableReason != UserVerificationUnavailableProviderUnavailable {
		t.Fatalf("websocket status result = %#v", status.Result)
	}
	assertUserVerificationUnavailable(t, router.Handle(requestWithParams(t, IntID(2), MethodUserVerificationEnroll, UserVerificationEnrollParams{})))
	assertUserVerificationUnavailable(t, router.Handle(requestWithParams(t, IntID(3), MethodUserVerificationDelete, UserVerificationDeleteParams{})))
	assertUserVerificationUnavailable(t, router.Handle(requestWithParams(t, IntID(4), MethodUserVerificationVerify, UserVerificationVerifyParams{
		Challenge:   "not-base64!!",
		Title:       "Confirm",
		Description: "Sign the challenge",
	})))

	local := NewRuntimeRouter(RuntimeServices{})
	local.SetRequestTransport("stdio")
	invalid := local.Handle(requestWithParams(t, IntID(5), MethodUserVerificationVerify, UserVerificationVerifyParams{
		Challenge:   "not-base64!!",
		Title:       "Confirm",
		Description: "Sign the challenge",
	}))
	if invalid.Error == nil || invalid.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("stdio invalid verify error = %+v", invalid.Error)
	}
}

// The provider seam carries a native backend's results to the wire responses;
// the default provider keeps the unsupported behavior.
func TestRuntimeRouterUserVerificationProviderSeamLikeRust(t *testing.T) {
	credentialID := "credential-1"
	provider := &stubUserVerificationProvider{status: &UserVerificationStatusResponse{CredentialID: &credentialID}}
	router := NewRuntimeRouter(RuntimeServices{UserVerificationProvider: provider})
	router.SetRequestTransport("stdio")

	status := router.Handle(requestWithParams(t, IntID(1), MethodUserVerificationStatus, UserVerificationStatusParams{}))
	if status.Error != nil {
		t.Fatalf("status error = %+v", status.Error)
	}
	if response, ok := status.Result.(*UserVerificationStatusResponse); !ok || response.CredentialID == nil || *response.CredentialID != credentialID {
		t.Fatalf("status result = %#v", status.Result)
	}
	enroll := router.Handle(requestWithParams(t, IntID(2), MethodUserVerificationEnroll, UserVerificationEnrollParams{}))
	if enroll.Error != nil {
		t.Fatalf("enroll error = %+v", enroll.Error)
	}
	if _, ok := enroll.Result.(*UserVerificationEnrollResponse); !ok {
		t.Fatalf("enroll result = %#v", enroll.Result)
	}
	verify := router.Handle(requestWithParams(t, IntID(3), MethodUserVerificationVerify, UserVerificationVerifyParams{
		Challenge:   base64.RawURLEncoding.EncodeToString([]byte("challenge-bytes")),
		Title:       "Confirm",
		Description: "Sign the challenge",
	}))
	if verify.Error != nil {
		t.Fatalf("verify error = %+v", verify.Error)
	}
	if _, ok := verify.Result.(*UserVerificationVerifyResponse); !ok {
		t.Fatalf("verify result = %#v", verify.Result)
	}
	if calls := provider.calls; len(calls) != 3 || calls[0] != "status" || calls[1] != "enroll" || calls[2] != "verify" {
		t.Fatalf("provider calls = %#v", calls)
	}
	if provider.verifyParams.Title != "Confirm" || provider.verifyParams.Description != "Sign the challenge" {
		t.Fatalf("verify params = %#v", provider.verifyParams)
	}
}

// stubUserVerificationProvider records the operations the handler dispatches.
type stubUserVerificationProvider struct {
	status       *UserVerificationStatusResponse
	calls        []string
	verifyParams UserVerificationVerifyParams
}

func (p *stubUserVerificationProvider) Status() (*UserVerificationStatusResponse, error) {
	p.calls = append(p.calls, "status")
	if p.status != nil {
		return p.status, nil
	}
	return userVerificationUnavailableStatus(), nil
}

func (p *stubUserVerificationProvider) EnsureKey() (*UserVerificationEnrollResponse, error) {
	p.calls = append(p.calls, "enroll")
	return &UserVerificationEnrollResponse{}, nil
}

func (p *stubUserVerificationProvider) Delete() (*UserVerificationDeleteResponse, error) {
	p.calls = append(p.calls, "delete")
	return &UserVerificationDeleteResponse{}, nil
}

func (p *stubUserVerificationProvider) Verify(params UserVerificationVerifyParams) (*UserVerificationVerifyResponse, error) {
	p.calls = append(p.calls, "verify")
	p.verifyParams = params
	return &UserVerificationVerifyResponse{}, nil
}

// Rust's Service::run drops CancelOnDrop when the RPC future ends, so a timed
// out worker is cancelled instead of leaving its native prompt up.
func TestUserVerificationWorkerTimeoutCancelsNativeWork(t *testing.T) {
	provider := &slowUserVerificationProvider{started: make(chan struct{}), unblock: make(chan struct{})}
	token := &userVerificationCancelToken{}
	service := &userVerificationService{provider: provider, cancel: token, timeout: 30 * time.Millisecond}
	defer close(provider.unblock)

	_, err := service.runNativeWork(func() (any, error) {
		return service.providerForRequest(service.cancellation()).Status()
	})
	detail, ok := err.(*userVerificationError)
	if !ok {
		t.Fatalf("timeout error = %#v", err)
	}
	if detail.data.Reason != string(UserVerificationFailureTimeout) || detail.message != "User verification timed out." {
		t.Fatalf("timeout error = %+v", detail)
	}
	if !token.IsCancelled() {
		t.Fatal("a timed out worker must be cancelled like Rust's dropped CancelOnDrop")
	}
}

// Rust re-checks the guard before handing a successful payload back, so work
// that finished after the request was cancelled never returns a stale proof.
func TestUserVerificationCancelledPayloadIsRejected(t *testing.T) {
	token := &userVerificationCancelToken{}
	provider := &slowUserVerificationProvider{started: make(chan struct{}), unblock: make(chan struct{}), cancel: token}
	service := &userVerificationService{provider: provider, cancel: token, timeout: 5 * time.Second}
	defer close(provider.unblock)

	_, err := service.runNativeWork(func() (any, error) {
		return service.providerForRequest(service.cancellation()).Status()
	})
	detail, ok := err.(*userVerificationError)
	if !ok {
		t.Fatalf("cancelled payload error = %#v", err)
	}
	if detail.data.Type != "cancelled" || detail.data.Reason != string(UserVerificationCancellationInterrupted) ||
		detail.message != userVerificationCancelledMessage {
		t.Fatalf("cancelled payload error = %+v", detail)
	}
}

// slowUserVerificationProvider blocks inside Status until unblock closes,
// optionally cancelling the request while it runs.
type slowUserVerificationProvider struct {
	started chan struct{}
	unblock chan struct{}
	cancel  *userVerificationCancelToken
}

func (p *slowUserVerificationProvider) Status() (*UserVerificationStatusResponse, error) {
	close(p.started)
	credentialID := "credential-1"
	if p.cancel != nil {
		// Finish the work after cancelling it, like a native operation whose
		// callback lands after the RPC already went away.
		p.cancel.Cancel()
		return &UserVerificationStatusResponse{CredentialID: &credentialID}, nil
	}
	<-p.unblock
	return &UserVerificationStatusResponse{CredentialID: &credentialID}, nil
}

func (p *slowUserVerificationProvider) EnsureKey() (*UserVerificationEnrollResponse, error) {
	return &UserVerificationEnrollResponse{}, nil
}

func (p *slowUserVerificationProvider) Delete() (*UserVerificationDeleteResponse, error) {
	return &UserVerificationDeleteResponse{}, nil
}

func (p *slowUserVerificationProvider) Verify(UserVerificationVerifyParams) (*UserVerificationVerifyResponse, error) {
	return &UserVerificationVerifyResponse{}, nil
}

// TestUserVerificationVerifyResponseBiometricTypeLikeRust pins Rust #60948:
// older servers omit biometricType and it stays empty, while a native Touch ID
// signature reports the fingerprint method alongside the proof.
func TestUserVerificationVerifyResponseBiometricTypeLikeRust(t *testing.T) {
	var older UserVerificationVerifyResponse
	if err := json.Unmarshal([]byte(`{"proof":{"credentialId":"credential","signature":"signature"}}`), &older); err != nil {
		t.Fatalf("decode older response: %v", err)
	}
	if older.BiometricType != nil {
		t.Fatalf("older response biometric type = %v, want empty", *older.BiometricType)
	}

	var newer UserVerificationVerifyResponse
	if err := json.Unmarshal([]byte(`{"proof":{"credentialId":"credential","signature":"signature"},"biometricType":"fingerprint"}`), &newer); err != nil {
		t.Fatalf("decode newer response: %v", err)
	}
	if newer.BiometricType == nil || *newer.BiometricType != UserVerificationBiometricFingerprint {
		t.Fatalf("biometric type = %v, want fingerprint", newer.BiometricType)
	}
}
