package provisionservice

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

const (
	// ProvisioningTimeout bounds one provisioning exchange.
	ProvisioningTimeout = 120 * time.Second
	// ServiceStartupTimeout bounds waiting for a STOPPED service to report
	// RUNNING before the caller falls back.
	ServiceStartupTimeout = 5 * time.Second
	// RegistrationTimeout bounds the uninstall-registration exchange.
	RegistrationTimeout = 5 * time.Second
	// MaxServiceRestarts keeps both gated registration refreshes inside the
	// original deadline (package registration may restart the service twice).
	MaxServiceRestarts = 2

	reconnectPollInterval = 25 * time.Millisecond
)

// Windows error numbers used by the client's reply classification. They never
// occur on other platforms; keeping them here lets the classification stay
// platform independent.
const (
	errnoBrokenPipe       = syscall.Errno(109)
	errnoNoData           = syscall.Errno(232)
	errnoPipeNotConnected = syscall.Errno(233)
)

// ErrServiceUnavailable reports that no provisioning service accepted the
// caller. Callers may fall back to the elevated helper (except registered Core).
var ErrServiceUnavailable = errors.New("sandbox provisioning service unavailable")

// Outcome is the result of attempting provisioning through the service.
type Outcome int

const (
	// Provisioned means the service completed elevated provisioning.
	Provisioned Outcome = iota
	// Unavailable means the service was absent, refused this caller, or timed
	// out; the caller may fall back to the elevated helper.
	Unavailable
)

// Intent selects full setup or a gated registration refresh.
type Intent int

const (
	IntentSetup Intent = iota
	IntentRefreshRegistration
)

// Conn is one authenticated connection to the provisioning service.
type Conn interface {
	// ServerPID identifies the pipe server process.
	ServerPID() uint32
	// Exchange authenticates the pipe server, writes the request and reads the
	// reply before the deadline.
	Exchange(frame *Frame, deadline time.Time) (Response, error)
	Close() error
}

// Transport dials the provisioning service.
type Transport interface {
	// Connect returns ErrServiceUnavailable when the service is absent.
	Connect(deadline time.Time) (Conn, error)
	// ConnectAfterRestart returns a pipe served by a different service process,
	// after that process finished starting.
	ConnectAfterRestart(previousPID uint32, deadline time.Time) (Conn, error)
}

// Client is the authenticated provisioning-service client.
type Client struct {
	Transport Transport
	// RegisteredCore reports the gated app-runtime selection.
	RegisteredCore func() bool
	// WorkloadIdentitySelected reports a process-local workload identity. The
	// IPC request cannot carry it, so setup selects the helper fallback.
	WorkloadIdentitySelected func() bool
	Now                      func() time.Time
	Sleep                    func(time.Duration)
}

// NewClient returns a client backed by the installed Windows service.
func NewClient() *Client {
	return &Client{Transport: NewWindowsTransport()}
}

// ProvisionWindowsSandbox provisions the elevated sandbox through the
// authenticated packaged service.
func (c *Client) ProvisionWindowsSandbox(codexHome string, settings Settings, listeners Listeners) (Outcome, error) {
	return c.provision(codexHome, settings, listeners, IntentSetup)
}

// RefreshRegisteredCore refreshes an already-provisioned gated runtime without
// creating or repairing sandbox accounts.
func (c *Client) RefreshRegisteredCore(codexHome string, settings Settings, listeners Listeners) (Outcome, error) {
	if !c.registeredCore() {
		return Unavailable, errors.New("registered Core is not enabled")
	}
	return c.provision(codexHome, settings, listeners, IntentRefreshRegistration)
}

// RegisterDesktopInstallation records desktop uninstall ownership without
// provisioning sandbox resources.
func (c *Client) RegisterDesktopInstallation(codexHome string) error {
	request := &Frame{
		Version: ProtocolVersion,
		Message: Message{Kind: KindRegisterInstallationRequest, CodexHome: canonicalizeProvisioningHome(codexHome)},
	}
	response, err := c.send(request, RegistrationTimeout)
	if err != nil {
		return err
	}
	switch response.Kind {
	case ResponseOK:
		return nil
	case ResponseUnavailable:
		return errors.New("desktop uninstall registration service is unavailable")
	case ResponseError:
		return errors.New(response.Message)
	default:
		return fmt.Errorf("unknown provisioning response status %q", response.Kind)
	}
}

func (c *Client) provision(codexHome string, settings Settings, listeners Listeners, intent Intent) (Outcome, error) {
	if c == nil || c.Transport == nil {
		return Unavailable, errors.New("sandbox provisioning transport is not configured")
	}
	if intent == IntentSetup && c.workloadIdentitySelected() {
		return c.unavailable()
	}
	request := &Frame{
		Version: ProtocolVersion,
		Message: Message{Kind: KindProvisionSandboxRequest, Request: &ProvisionRequest{
			CodexHome:      canonicalizeProvisioningHome(codexHome),
			RegisteredCore: c.registeredCore(),
			RefreshOnly:    intent == IntentRefreshRegistration,
			Settings:       settings,
			Listeners:      listeners,
		}},
	}
	response, err := c.send(request, ProvisioningTimeout)
	if err != nil {
		return Unavailable, err
	}
	switch response.Kind {
	case ResponseOK:
		return Provisioned, nil
	case ResponseUnavailable:
		return c.unavailable()
	case ResponseError:
		return Unavailable, errors.New(response.Message)
	default:
		return Unavailable, fmt.Errorf("unknown provisioning response status %q", response.Kind)
	}
}

func (c *Client) send(request *Frame, timeout time.Duration) (Response, error) {
	deadline := c.now().Add(timeout)
	pipe, err := c.Transport.Connect(deadline)
	if err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			return Response{Kind: ResponseUnavailable}, nil
		}
		return Response{}, err
	}
	if pipe == nil {
		return Response{Kind: ResponseUnavailable}, nil
	}
	if payload := request.Message.Request; payload != nil && payload.RegisteredCore && payload.RefreshOnly {
		return c.sendWithRestartRetry(pipe, request, deadline)
	}
	response, err := pipe.Exchange(request, deadline)
	if err != nil {
		_ = pipe.Close()
		if isUnavailableExchangeError(err) {
			return Response{Kind: ResponseUnavailable}, nil
		}
		return Response{}, err
	}
	if response.Kind == ResponseError && response.Message == GroupChanged {
		_ = pipe.Close()
		return c.retryAfterGroupChange(request, deadline)
	}
	_ = pipe.Close()
	return response, nil
}

// sendWithRestartRetry resumes a gated registration refresh only after an
// authenticated service restart. Only reply-side pipe disconnects qualify;
// failed authentication, failed writes, explicit replies and protocol errors
// always return.
func (c *Client) sendWithRestartRetry(pipe Conn, request *Frame, deadline time.Time) (Response, error) {
	restartsRemaining := MaxServiceRestarts
	for {
		if err := c.remainingTime(deadline); err != nil {
			_ = pipe.Close()
			return Response{}, err
		}
		serverPID := pipe.ServerPID()
		response, err := pipe.Exchange(request, deadline)
		if !c.takeRestart(err, &restartsRemaining, deadline) {
			_ = pipe.Close()
			if err != nil {
				return Response{}, err
			}
			return response, nil
		}
		_ = pipe.Close()
		pipe, err = c.Transport.ConnectAfterRestart(serverPID, deadline)
		if err != nil {
			return Response{}, err
		}
	}
}

// retryAfterGroupChange retries only the service's authenticated pre-dispatch
// refusal. A lost response may follow a mutation, so retry transport errors
// stay errors.
func (c *Client) retryAfterGroupChange(request *Frame, deadline time.Time) (Response, error) {
	var pipe Conn
	for {
		connected, err := c.Transport.Connect(deadline)
		switch {
		case err != nil && !errors.Is(err, ErrServiceUnavailable):
			return Response{}, err
		case err == nil && connected != nil:
			pipe = connected
		}
		if pipe != nil {
			break
		}
		if err := c.remainingTime(deadline); err != nil {
			return Response{}, errors.New("refreshed sandbox service pipe is unavailable")
		}
		c.sleep(reconnectPollInterval)
	}
	response, err := pipe.Exchange(request, deadline)
	_ = pipe.Close()
	if err != nil {
		return Response{}, err
	}
	return response, nil
}

// takeRestart consumes one restart budget for a reply-side pipe disconnect.
// Explicit replies never consume a restart because the caller passes nil.
func (c *Client) takeRestart(err error, restartsRemaining *int, deadline time.Time) bool {
	if err == nil {
		return false
	}
	if *restartsRemaining == 0 || !c.now().Before(deadline) || !isResponseDisconnect(err) {
		return false
	}
	*restartsRemaining--
	return true
}

func (c *Client) remainingTime(deadline time.Time) error {
	if !c.now().Before(deadline) {
		return fmt.Errorf("timed out waiting for restarted sandbox provisioning service: %w", os.ErrDeadlineExceeded)
	}
	return nil
}

// isUnavailableExchangeError matches Rust's exchange classification: a closed
// or silent pipe and a timeout mean the caller may fall back.
func isUnavailableExchangeError(err error) bool {
	if isResponseDisconnect(err) {
		return true
	}
	return errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, syscall.ETIMEDOUT)
}

// isResponseDisconnect matches Rust's NotFound/UnexpectedEof/BrokenPipe set.
func isResponseDisconnect(err error) bool {
	if err == nil {
		return false
	}
	for _, target := range []error{
		io.EOF,
		io.ErrUnexpectedEOF,
		syscall.EPIPE,
		errnoBrokenPipe,
		errnoNoData,
		errnoPipeNotConnected,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

func (c *Client) unavailable() (Outcome, error) {
	if c.registeredCore() {
		return Unavailable, errors.New("app runtime provisioning service is unavailable; refusing helper fallback")
	}
	return Unavailable, nil
}

func (c *Client) registeredCore() bool {
	if c != nil && c.RegisteredCore != nil {
		return c.RegisteredCore()
	}
	return false
}

func (c *Client) workloadIdentitySelected() bool {
	if c != nil && c.WorkloadIdentitySelected != nil {
		return c.WorkloadIdentitySelected()
	}
	return os.Getenv("OPENAI_FEDERATION_RULE_ID") != "" || os.Getenv("OPENAI_IDENTITY_TOKEN_FILE") != ""
}

func (c *Client) now() time.Time {
	if c != nil && c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) sleep(duration time.Duration) {
	if c != nil && c.Sleep != nil {
		c.Sleep(duration)
		return
	}
	time.Sleep(duration)
}
