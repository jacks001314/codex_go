package appserver

import (
	"fmt"
	"strings"
)

// Runtime handlers for the experimental user-verification RPCs.
//
// Rust resolves a platform provider (codex-user-verification's
// `platform_provider`) and reports `providerUnavailable` when the build has no
// native backend; its app-server service also gates which operations a peer may
// run by connection origin. Go mirrors both: the provider seam below is
// installable through RuntimeServices, and the default provider keeps the
// behavior of a Rust build without a native backend.

// UserVerificationProvider mirrors codex-user-verification's native provider
// trait: the platform backend performing one captured identity's local
// credential operations. Implementations never perform network registration.
type UserVerificationProvider interface {
	// Status reads local readiness without creating credentials or prompting.
	Status() (*UserVerificationStatusResponse, error)
	// EnsureKey creates a protected key only if none exists; success is not
	// server enrollment.
	EnsureKey() (*UserVerificationEnrollResponse, error)
	// Delete removes the local key idempotently.
	Delete() (*UserVerificationDeleteResponse, error)
	// Verify authenticates and signs the validated challenge.
	Verify(params UserVerificationVerifyParams) (*UserVerificationVerifyResponse, error)
}

// UnsupportedUserVerificationProvider is Rust's
// `unsupported::UnsupportedProvider`: every operation reports
// `providerUnavailable`.
type UnsupportedUserVerificationProvider struct{}

func (UnsupportedUserVerificationProvider) Status() (*UserVerificationStatusResponse, error) {
	return userVerificationUnavailableStatus(), nil
}

func (UnsupportedUserVerificationProvider) EnsureKey() (*UserVerificationEnrollResponse, error) {
	return nil, userVerificationUnavailableError()
}

func (UnsupportedUserVerificationProvider) Delete() (*UserVerificationDeleteResponse, error) {
	return nil, userVerificationUnavailableError()
}

func (UnsupportedUserVerificationProvider) Verify(UserVerificationVerifyParams) (*UserVerificationVerifyResponse, error) {
	return nil, userVerificationUnavailableError()
}

// userVerificationService mirrors the app-server's user-verification Service:
// the connection origin decides which operations a peer may run, and the
// platform provider performs them.
type userVerificationService struct {
	provider UserVerificationProvider
}

// userVerificationPeerRestricted reports whether the transport is a network
// peer, which may only read status: a peer must sign on its own device
// (Rust's ConnectionOrigin::WebSocket | RemoteControl arm).
func userVerificationPeerRestricted(transport string) bool {
	switch strings.ToLower(strings.TrimSpace(transport)) {
	case "websocket", "remote_control", "remote-control", "remotecontrol":
		return true
	default:
		return false
	}
}

func (s *userVerificationService) platformProvider() UserVerificationProvider {
	if s == nil || s.provider == nil {
		return UnsupportedUserVerificationProvider{}
	}
	return s.provider
}

// handle runs one operation in Rust's order: validate and gate the origin
// before any provider work, then dispatch to the provider.
func (s *userVerificationService) handle(request *Request, transport string) (any, error) {
	switch request.Method {
	case MethodUserVerificationStatus:
		// Decode the (empty) params like Rust so an unknown field is rejected
		// instead of being ignored.
		var params UserVerificationStatusParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		return s.platformProvider().Status()
	case MethodUserVerificationEnroll:
		var params UserVerificationEnrollParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if userVerificationPeerRestricted(transport) {
			return nil, userVerificationUnavailableError()
		}
		return s.platformProvider().EnsureKey()
	case MethodUserVerificationDelete:
		var params UserVerificationDeleteParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if userVerificationPeerRestricted(transport) {
			return nil, userVerificationUnavailableError()
		}
		return s.platformProvider().Delete()
	case MethodUserVerificationVerify:
		var params UserVerificationVerifyParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		// Rust gates the origin before validating, so a peer's malformed
		// challenge still reports providerUnavailable.
		if userVerificationPeerRestricted(transport) {
			return nil, userVerificationUnavailableError()
		}
		if err := validateUserVerificationRequest(&params); err != nil {
			return nil, err
		}
		return s.platformProvider().Verify(params)
	case MethodUserVerificationCancel:
		var params UserVerificationCancelParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		// No native work is ever started by the unsupported provider, so
		// cancellation is a no-op ack.
		return &UserVerificationCancelResponse{}, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, request.Method)
	}
}

func isUserVerificationMethod(method Method) bool {
	switch method {
	case MethodUserVerificationStatus, MethodUserVerificationEnroll,
		MethodUserVerificationDelete, MethodUserVerificationVerify,
		MethodUserVerificationCancel:
		return true
	default:
		return false
	}
}

func (r *RuntimeRouter) handleUserVerificationRuntime(request *Request) (any, error) {
	if r == nil || request == nil {
		return nil, fmt.Errorf("%w: user verification request is nil", ErrUnknownMethod)
	}
	service := &userVerificationService{provider: r.services.UserVerificationProvider}
	return service.handle(request, r.requestTransport)
}
