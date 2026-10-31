//go:build windows

package windowsandboxservice

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"codex_go/sandbox/windowssandbox/provisionservice"
)

const (
	requestIdleTimeout       = 5 * time.Second
	requestPollInterval      = 25 * time.Millisecond
	responseClosePollPeriod  = 10 * time.Millisecond
	responseCloseGracePeriod = time.Second
	frameLengthPrefixBytes   = 4
)

var procPeekNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

// PolicyValidator checks one authenticated provisioning request against
// administrator-controlled machine policy while acting as the requesting user.
type PolicyValidator func(codexHome string, request *ProvisioningRequest, impersonationToken windows.Token) error

// AuthenticatedRequestHandler performs one authenticated request.
type AuthenticatedRequestHandler func(identity *ClientIdentity, request *ServiceRequest, sandboxSID []byte, stopped func() bool) (provisionservice.Response, error)

// ReadProvisioningRequest accumulates exactly one request frame from the pipe
// (Rust ipc::handle_request's PeekNamedPipe loop): at most MaxRequestBytes, no
// empty read, no second frame and a 5 s idle deadline.
func ReadProvisioningRequest(pipe windows.Handle, stopped func() bool) ([]byte, error) {
	deadline := time.Now().Add(requestIdleTimeout)
	request := make([]byte, MaxRequestBytes)
	length := 0
	for {
		if stopped != nil && stopped() {
			return nil, errors.New("service is stopping")
		}
		if !time.Now().Before(deadline) {
			return nil, errors.New("provisioning request timed out")
		}
		available, err := peekAvailable(pipe)
		if err != nil {
			return nil, fmt.Errorf("inspect provisioning request: %w", err)
		}
		if int(available) > MaxRequestBytes-length {
			return nil, errors.New("provisioning request exceeds size limit")
		}
		if available == 0 {
			time.Sleep(requestPollInterval)
			continue
		}
		var read uint32
		if err := windows.ReadFile(pipe, request[length:length+int(available)], &read, nil); err != nil {
			return nil, fmt.Errorf("read provisioning request: %w", err)
		}
		if read == 0 {
			return nil, errors.New("provisioning client sent an empty request")
		}
		length += int(read)
		if length < frameLengthPrefixBytes {
			continue
		}
		payloadLength := binary.LittleEndian.Uint32(request[:frameLengthPrefixBytes])
		if uint64(payloadLength) > MaxRequestBytes-frameLengthPrefixBytes {
			return nil, errors.New("provisioning request exceeds size limit")
		}
		frameLength := frameLengthPrefixBytes + int(payloadLength)
		if length > frameLength {
			return nil, errors.New("provisioning requests must contain exactly one IPC frame")
		}
		if length == frameLength {
			return request[:length], nil
		}
	}
}

// WriteResponse sends one frame and waits briefly for the client to close so a
// following DisconnectNamedPipe does not truncate the response (Rust
// ipc::write_response).
func WriteResponse(pipe windows.Handle, response provisionservice.Response, stopped func() bool) error {
	frame := &provisionservice.Frame{
		Version: provisionservice.ProtocolVersion,
		Message: provisionservice.Message{
			Kind:     provisionservice.KindProvisionSandboxResponse,
			Response: &response,
		},
	}
	var buffer bytes.Buffer
	if err := provisionservice.WriteFrame(&buffer, frame); err != nil {
		return fmt.Errorf("serialize sandbox provisioning response: %w", err)
	}
	var written uint32
	if err := windows.WriteFile(pipe, buffer.Bytes(), &written, nil); err != nil {
		return fmt.Errorf("write sandbox provisioning response: %w", err)
	}
	if written == 0 {
		return nil
	}
	deadline := time.Now().Add(responseCloseGracePeriod)
	for (stopped == nil || !stopped()) && time.Now().Before(deadline) {
		if _, err := peekAvailable(pipe); err != nil {
			break
		}
		time.Sleep(responseClosePollPeriod)
	}
	return nil
}

// HandleRequest reads, validates, authenticates, policy-checks and dispatches one
// request. A request the service cannot serve becomes Unavailable; every other
// failure becomes a bounded Error (Rust ipc::handle_request plus ipc::run's
// response mapping). The only non-nil error is ErrRuntimeSetupInterrupted,
// which must escape without answering the client.
func HandleRequest(
	pipe windows.Handle,
	process *AuthorizedClientProcess,
	sandboxSID []byte,
	stopped func() bool,
	policy PolicyValidator,
	handler AuthenticatedRequestHandler,
) (provisionservice.Response, error) {
	raw, err := ReadProvisioningRequest(pipe, stopped)
	if err != nil {
		return failureResponse(err), nil
	}
	request, err := ValidateRequest(raw)
	if err != nil {
		return failureResponse(err), nil
	}
	identity, err := AuthenticateClient(pipe, process, sandboxSID, request)
	if err != nil {
		return failureResponse(err), nil
	}
	defer identity.Close()
	if err := validateProvisioningPolicy(identity, request, policy); err != nil {
		return policyViolationResponse(err), nil
	}
	if handler == nil {
		return failureResponse(errors.New("the provisioning service has no request handler")), nil
	}
	response, err := handler(identity, request, sandboxSID, stopped)
	if err != nil {
		if errors.Is(err, ErrRuntimeSetupInterrupted) {
			return provisionservice.Response{}, err
		}
		return failureResponse(err), nil
	}
	return response, nil
}

func validateProvisioningPolicy(identity *ClientIdentity, request *ServiceRequest, policy PolicyValidator) error {
	// Registration does not provision resources or change sandbox policy, and a
	// registered refresh retains the existing sandbox (Rust machine_policy).
	if request.Register != nil {
		return nil
	}
	if request.Provision.RegisteredCore && request.Provision.RefreshOnly {
		return nil
	}
	if policy == nil {
		return errors.New("no managed-policy validator is configured")
	}
	return policy(identity.CodexHome, request.Provision, identity.Token)
}

func failureResponse(err error) provisionservice.Response {
	if err == nil {
		return provisionservice.Response{Kind: provisionservice.ResponseOK}
	}
	if IsServiceUnavailable(err) {
		return provisionservice.Response{Kind: provisionservice.ResponseUnavailable}
	}
	return provisionservice.Response{Kind: provisionservice.ResponseError, Message: ErrorMessage(err)}
}

func peekAvailable(pipe windows.Handle) (uint32, error) {
	var available uint32
	ok, _, callErr := procPeekNamedPipe.Call(
		uintptr(pipe),
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&available)),
		0,
	)
	if ok == 0 {
		return 0, callErr
	}
	return available, nil
}
