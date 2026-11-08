package appserver

import "testing"

// Mirrors Rust's ConnectionOrigin::WebSocket | RemoteControl arm: a network
// peer may read status but must never trigger a native signature, delete a
// credential, or enroll one. The websocket case already has a dedicated test;
// this one pins the remote-control transport and the local transports that must
// stay unrestricted.
func TestUserVerificationRemoteControlPeerRestrictedLikeRust(t *testing.T) {
	for _, transport := range []string{"remote_control", "remote-control", "remotecontrol"} {
		router := NewRuntimeRouter(RuntimeServices{})
		router.SetRequestTransport(transport)

		status := router.Handle(requestWithParams(t, IntID(1), MethodUserVerificationStatus, UserVerificationStatusParams{}))
		if status.Error != nil {
			t.Fatalf("%s status error = %+v", transport, status.Error)
		}
		response, ok := status.Result.(*UserVerificationStatusResponse)
		if !ok || response.UnavailableReason == nil || *response.UnavailableReason != UserVerificationUnavailableProviderUnavailable {
			t.Fatalf("%s status result = %#v, want the peer-restricted surface", transport, status.Result)
		}
		assertUserVerificationUnavailable(t, router.Handle(requestWithParams(t, IntID(2), MethodUserVerificationEnroll, UserVerificationEnrollParams{})))
		assertUserVerificationUnavailable(t, router.Handle(requestWithParams(t, IntID(3), MethodUserVerificationDelete, UserVerificationDeleteParams{})))
		assertUserVerificationUnavailable(t, router.Handle(requestWithParams(t, IntID(4), MethodUserVerificationVerify, UserVerificationVerifyParams{
			Challenge:   "not-base64!!",
			Title:       "Confirm",
			Description: "Sign the challenge",
		})))
	}

	// The same invalid challenge over a local transport reaches validation, which
	// proves the peer restriction is what blocked the network requests above.
	local := NewRuntimeRouter(RuntimeServices{})
	local.SetRequestTransport("stdio")
	invalid := local.Handle(requestWithParams(t, IntID(5), MethodUserVerificationVerify, UserVerificationVerifyParams{
		Challenge:   "not-base64!!",
		Title:       "Confirm",
		Description: "Sign the challenge",
	}))
	if invalid.Error == nil || invalid.Error.Code != JSONRPCInvalidParamsErrorCode {
		t.Fatalf("stdio invalid verify error = %+v, want invalid params", invalid.Error)
	}
}
