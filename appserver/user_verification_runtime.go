package appserver

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codex_go/auth"
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

// userVerificationPlatformDeviceProbe is Rust's crate-level
// native::device_supported(): the platform hardware probe, independent of any
// injected provider seam. Tests replace it to assert the initialize gate really
// consults the platform.
var userVerificationPlatformDeviceProbe = func() bool {
	support, ok := newUserVerificationPlatformProvider().(userVerificationDeviceSupport)
	return ok && support.DeviceSupported()
}

// userVerificationDeviceSupported reports Rust's crate-level device probe
// (native::device_supported) for one router. NewDefaultRuntimeRouterWithOptions
// installs newUserVerificationPlatformProvider(), so the injected provider
// normally answers; the nil branch below is a compatibility path for hand-built
// RuntimeServices (tests and embedders that wire services individually) and is
// no longer the production source of truth.
func userVerificationDeviceSupported(provider UserVerificationProvider) bool {
	if provider == nil {
		return userVerificationPlatformDeviceProbe()
	}
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
	// account supplies the captured ChatGPT account-user identity Rust uses to
	// namespace the platform credential. Nil keeps providers unbound.
	account *auth.AccountManager
	// acquire and release own Rust #43568's one-permit native worker slot. A nil
	// pair leaves the service unguarded, which only direct test construction does.
	acquire func() bool
	release func()
	// timeout overrides userVerificationWorkerTimeout in tests.
	timeout time.Duration
	// cancel is this request's cancellation guard; nil for cancel/ack.
	cancel *userVerificationCancelToken
	// activity is Rust's with_activity_check guard (connection, credential
	// revision, captured identity). Nil leaves the token as the only
	// cancellation source, which only direct test construction does.
	activity *userVerificationActivity
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
		if s.cancelled() {
			// Rust checks the guard before any provider work
			// (app-server/src/user_verification.rs:174).
			results <- userVerificationWorkResult{
				err: userVerificationCancelledError(UserVerificationCancellationInterrupted),
			}
			return
		}
		value, err := run()
		results <- userVerificationWorkResult{value: value, err: err}
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-results:
		if result.err == nil && s.cancelled() {
			// Rust re-checks the guard before delivering a successful payload
			// (Service::run's `cancel.0.check()`), so a peer that cancelled or a
			// turn that ended mid-operation never sees a stale proof.
			return nil, userVerificationCancelledError(UserVerificationCancellationInterrupted)
		}
		return result.value, result.err
	case <-timer.C:
		// Rust drops CancelOnDrop when the RPC future completes, which cancels
		// the worker; the native prompt must be invalidated rather than left up.
		if s != nil && s.cancel != nil {
			s.cancel.Cancel()
		}
		return nil, newUserVerificationError(
			UserVerificationErrorDetails{Type: "failed", Reason: string(UserVerificationFailureTimeout)},
			"User verification timed out.",
			false,
		)
	}
}

// cancelled reports whether this request's guard was cancelled while the native
// worker ran.
func (s *userVerificationService) cancelled() bool {
	if s == nil {
		return false
	}
	if s.activity != nil {
		return !s.activity.active()
	}
	return s.cancel != nil && s.cancel.IsCancelled()
}

// userVerificationActivity mirrors Rust's UserVerificationRequestGuard::
// with_activity_check closure (codex-rs/app-server/src/user_verification.rs:96-101):
// a request stays live only while its connection is open, its credential
// revision is unchanged, and the captured account-user identity still matches.
type userVerificationActivity struct {
	router       *RuntimeRouter
	connectionID string
	authState    auth.AuthChangeState
	identity     string
	token        *userVerificationCancelToken
}

// active reports whether every Rust condition still holds. It must stay
// non-blocking: native providers poll it from their 50 ms wait loops
// (codex-rs/user-verification/src/native_operation.rs:15-31) and from
// guard.check() checkpoints around the security-framework calls.
func (a *userVerificationActivity) active() bool {
	if a == nil {
		return true
	}
	if a.token != nil && a.token.IsCancelled() {
		return false
	}
	if a.router == nil {
		return true
	}
	if a.router.connectionClosed(a.connectionID) {
		return false
	}
	// Rust compares the whole AuthChangeState, so an ordinary credential
	// refresh cancels in-flight work as well.
	if a.router.authChangeTracker != nil && a.router.authChangeTracker.Snapshot() != a.authState {
		return false
	}
	// The identity check keeps a credential swap that bypassed the tracker from
	// signing under the previous account-user namespace.
	return userVerificationAccountUserID(a.router.services.Account) == a.identity
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

// userVerificationRequestBinder is the optional seam a native provider uses to
// receive the captured account identity and this request's cancellation flag.
type userVerificationRequestBinder interface {
	BindUserVerificationRequest(accountUserID string, cancelled func() bool) UserVerificationProvider
}

// userVerificationAccountUserID mirrors Rust's identity(auth_manager):
// the opaque account-user claim is used only when it matches the selected
// workspace.
func userVerificationAccountUserID(manager *auth.AccountManager) string {
	if manager == nil {
		return ""
	}
	return auth.ChatGPTAccountUserIDFromAuth(manager.AuthSnapshot())
}

func (s *userVerificationService) platformProvider() UserVerificationProvider {
	if s == nil || s.provider == nil {
		return newUserVerificationPlatformProvider()
	}
	return s.provider
}

// providerForRequest binds the account namespace and cancellation token for
// identity-aware providers; plain providers (tests and non-native backends)
// keep receiving the request directly.
func (s *userVerificationService) cancellation() func() bool {
	if s == nil {
		return nil
	}
	if s.activity != nil {
		return func() bool { return !s.activity.active() }
	}
	if s.cancel == nil {
		return nil
	}
	return s.cancel.IsCancelled
}

func (s *userVerificationService) providerForRequest(cancelled func() bool) UserVerificationProvider {
	provider := s.platformProvider()
	if binder, ok := provider.(userVerificationRequestBinder); ok {
		return binder.BindUserVerificationRequest(userVerificationAccountUserID(s.account), cancelled)
	}
	return provider
}

// userVerificationCancelToken is the per-request guard. Rust cancels blocking
// native work when the RPC future is dropped; Go exposes the same cancellation
// through userVerification/cancel and checks it before every effect.
type userVerificationCancelToken struct {
	cancelled atomic.Bool
}

func (t *userVerificationCancelToken) Cancel() {
	if t != nil {
		t.cancelled.Store(true)
	}
}

func (t *userVerificationCancelToken) IsCancelled() bool {
	return t != nil && t.cancelled.Load()
}

// userVerificationCancels maps a JSON-RPC request id to its in-flight
// operation. A cancelled id stays until the operation returns, so a late
// cancel cannot affect a later request with a recycled id.
var userVerificationCancels sync.Map

func userVerificationCancelKey(id RequestID) string {
	if id.IsZero() {
		return ""
	}
	return fmt.Sprintf("%T:%s", id.value, id.String())
}

func registerUserVerificationCancel(id RequestID, token *userVerificationCancelToken) func() {
	key := userVerificationCancelKey(id)
	if key == "" {
		return func() {}
	}
	userVerificationCancels.Store(key, token)
	return func() { userVerificationCancels.Delete(key) }
}

func cancelUserVerificationRequest(id RequestID) {
	key := userVerificationCancelKey(id)
	if key == "" {
		return
	}
	if token, ok := userVerificationCancels.Load(key); ok {
		if cancel, ok := token.(*userVerificationCancelToken); ok {
			cancel.Cancel()
		}
	}
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
			return s.providerForRequest(s.cancellation()).Status()
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
			return s.providerForRequest(s.cancellation()).EnsureKey()
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
			return s.providerForRequest(s.cancellation()).Delete()
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
			return s.providerForRequest(s.cancellation()).Verify(params)
		})
	case MethodUserVerificationCancel:
		var params UserVerificationCancelParams
		if err := request.DecodeParams(&params); err != nil {
			return nil, err
		}
		// Cancellation is acknowledged even for an unknown request id, matching
		// Rust's cancel handler.
		cancelUserVerificationRequest(params.RequestID)
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
	service := &userVerificationService{
		provider: r.services.UserVerificationProvider,
		account:  r.services.Account,
	}
	service.acquire = r.acquireUserVerificationWork
	service.release = r.releaseUserVerificationWork
	if request.Method != MethodUserVerificationCancel {
		service.cancel = &userVerificationCancelToken{}
		service.activity = &userVerificationActivity{
			router:       r,
			connectionID: request.normalizedConnectionID(),
			authState:    r.authChangeTracker.Snapshot(),
			identity:     userVerificationAccountUserID(r.services.Account),
			token:        service.cancel,
		}
		unregister := registerUserVerificationCancel(request.ID, service.cancel)
		defer unregister()
	}
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
