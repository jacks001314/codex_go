package appserver

import (
	"fmt"
	"strings"
	"time"
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

// userVerificationDeviceSupport is the optional platform probe used when a
// trusted local client initializes. Rust's initialize processor only activates
// the extension when Service::device_supported() is true (#44613); providers
// without a native backend omit this method and remain ineligible.
type userVerificationDeviceSupport interface {
	DeviceSupported() bool
}

func userVerificationDeviceSupported(provider UserVerificationProvider) bool {
	support, ok := provider.(userVerificationDeviceSupport)
	return ok && support.DeviceSupported()
}

// UnsupportedUserVerificationProvider is Rust's
// `unsupported::UnsupportedProvider`: every operation reports
// `providerUnavailable`.
type UnsupportedUserVerificationProvider struct{}

func (UnsupportedUserVerificationProvider) DeviceSupported() bool {
	return false
}

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
	// acquire and release own Rust #43568's one-permit native worker slot. A nil
	// pair leaves the service unguarded, which only direct test construction does.
	acquire func() bool
	release func()
	// timeout overrides userVerificationWorkerTimeout in tests.
	timeout time.Duration
}

// userVerificationWorkerTimeout bounds one native provider operation, matching
// Rust #43568's 120-second worker timeout.
const userVerificationWorkerTimeout = 120 * time.Second

// userVerificationWorkResult carries one worker's provider result back to the
// request goroutine.
type userVerificationWorkResult struct {
	value any
	err   error
}

// runNativeWork runs one provider operation on its own goroutine. Rust keeps a
// single native worker per service, rejects a concurrent operation with a
// provider error, and times the work out after 120 seconds; the worker slot
// stays taken until the provider actually returns, including after a timeout.
func (s *userVerificationService) runNativeWork(run func() (any, error)) (any, error) {
	if s != nil && s.acquire != nil && !s.acquire() {
		return nil, newUserVerificationError(
			UserVerificationErrorDetails{Type: "failed", Reason: string(UserVerificationFailureProviderError)},
			"Another user verification operation is still running.",
			false,
		)
	}
	release := func() {}
	if s != nil && s.release != nil {
		release = s.release
	}
	timeout := userVerificationWorkerTimeout
	if s != nil && s.timeout > 0 {
		timeout = s.timeout
	}
	results := make(chan userVerificationWorkResult, 1)
	go func() {
		defer release()
		value, err := run()
		results <- userVerificationWorkResult{value: value, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-results:
		return result.value, result.err
	case <-timer.C:
		return nil, newUserVerificationError(
			UserVerificationErrorDetails{Type: "failed", Reason: string(UserVerificationFailureTimeout)},
			"User verification timed out.",
			false,
		)
	}
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
		return s.runNativeWork(func() (any, error) {
			return s.platformProvider().Status()
		})
	case MethodUserVerificationEnroll:
		var params UserVerificationEnrollParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if userVerificationPeerRestricted(transport) {
			return nil, userVerificationUnavailableError()
		}
		return s.runNativeWork(func() (any, error) {
			return s.platformProvider().EnsureKey()
		})
	case MethodUserVerificationDelete:
		var params UserVerificationDeleteParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		if userVerificationPeerRestricted(transport) {
			return nil, userVerificationUnavailableError()
		}
		return s.runNativeWork(func() (any, error) {
			return s.platformProvider().Delete()
		})
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
		return s.runNativeWork(func() (any, error) {
			return s.platformProvider().Verify(params)
		})
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
	service.acquire = r.acquireUserVerificationWork
	service.release = r.releaseUserVerificationWork
	return service.handle(request, r.requestTransport)
}

// acquireUserVerificationWork takes Rust #43568's single native worker slot,
// reporting false when another operation still owns it.
func (r *RuntimeRouter) acquireUserVerificationWork() bool {
	if r == nil {
		return true
	}
	r.userVerificationWorkMu.Lock()
	defer r.userVerificationWorkMu.Unlock()
	if r.userVerificationWork {
		return false
	}
	r.userVerificationWork = true
	return true
}

// releaseUserVerificationWork returns the native worker slot.
func (r *RuntimeRouter) releaseUserVerificationWork() {
	if r == nil {
		return
	}
	r.userVerificationWorkMu.Lock()
	r.userVerificationWork = false
	r.userVerificationWorkMu.Unlock()
}
