package appserverdaemon

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"time"

	"codex_go/remotecontrol"
)

const (
	ClientName                   = "codex_app_server_daemon"
	InitializeRequestID    int64 = 1
	RemoteControlRequestID int64 = 2
	InvalidParamsErrorCode       = -32602

	RemoteControlReadyTimeout = 10 * time.Second
	ControlSocketProbeTimeout = 200 * time.Millisecond
)

var errRemoteControlInvalidParams = errors.New("remote-control request returned invalid params")

type ClientInfo struct {
	Name    string  `json:"name"`
	Title   *string `json:"title,omitempty"`
	Version string  `json:"version"`
}

type InitializeCapabilities struct {
	ExperimentalAPI bool `json:"experimentalApi,omitempty"`
}

type InitializeParams struct {
	ClientInfo   ClientInfo              `json:"clientInfo"`
	Capabilities *InitializeCapabilities `json:"capabilities,omitempty"`
}

type InitializeResponse struct {
	UserAgent string `json:"userAgent"`
}

type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc,omitempty"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type JSONRPCNotification struct {
	JSONRPC string `json:"jsonrpc,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type JSONRPCError struct {
	ID    int64 `json:"id"`
	Error struct {
		Code    int64  `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type RemoteControlRPCResultKind string

const (
	RemoteControlRPCSuccess       RemoteControlRPCResultKind = "success"
	RemoteControlRPCInvalidParams RemoteControlRPCResultKind = "invalidParams"
	RemoteControlRPCOtherError    RemoteControlRPCResultKind = "otherError"
	RemoteControlRPCIgnored       RemoteControlRPCResultKind = "ignored"
)

type RemoteControlRequestAttempt struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type RemoteControlMessageKind string

const (
	RemoteControlMessageResponse           RemoteControlMessageKind = "response"
	RemoteControlMessageError              RemoteControlMessageKind = "error"
	RemoteControlMessageStatusNotification RemoteControlMessageKind = "statusNotification"
	RemoteControlMessageIgnored            RemoteControlMessageKind = "ignored"
)

type RemoteControlRPCMessage struct {
	Kind         RemoteControlMessageKind
	Result       json.RawMessage
	ErrorCode    int64
	ErrorMessage string
	Status       *RemoteControlReadyStatus
}

func BuildInitializeRequest(version string, experimentalAPI bool) JSONRPCRequest {
	title := "Codex App Server Daemon"
	var capabilities *InitializeCapabilities
	if experimentalAPI {
		capabilities = &InitializeCapabilities{ExperimentalAPI: true}
	}
	return JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      InitializeRequestID,
		Method:  "initialize",
		Params: InitializeParams{
			ClientInfo: ClientInfo{
				Name:    ClientName,
				Title:   &title,
				Version: version,
			},
			Capabilities: capabilities,
		},
	}
}

func RemoteControlRequestAttempts(method string, params any, firstResult RemoteControlRPCResultKind) []RemoteControlRequestAttempt {
	attempts := []RemoteControlRequestAttempt{{Method: method, Params: params}}
	if firstResult == RemoteControlRPCInvalidParams {
		attempts = append(attempts, RemoteControlRequestAttempt{Method: method})
	}
	return attempts
}

func EnableRemoteControlAttempts(firstResult RemoteControlRPCResultKind) []RemoteControlRequestAttempt {
	return RemoteControlRequestAttempts("remoteControl/enable", remotecontrol.EnableParams{Ephemeral: true}, firstResult)
}

func DisableRemoteControlAttempts(firstResult RemoteControlRPCResultKind) []RemoteControlRequestAttempt {
	return RemoteControlRequestAttempts("remoteControl/disable", remotecontrol.DisableParams{Ephemeral: true}, firstResult)
}

func PairingStartRequest() RemoteControlRequestAttempt {
	return RemoteControlRequestAttempt{
		Method: "remoteControl/pairing/start",
		Params: remotecontrol.PairingStartParams{ManualCode: true},
	}
}

func BuildRemoteControlRequest(method string, params any) JSONRPCRequest {
	return JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      RemoteControlRequestID,
		Method:  method,
		Params:  params,
	}
}

func InitializedNotification() JSONRPCNotification {
	return JSONRPCNotification{JSONRPC: "2.0", Method: "initialized"}
}

func ParseVersionFromUserAgent(userAgent string) (string, error) {
	_, rest, ok := strings.Cut(userAgent, "/")
	if !ok {
		return "", fmt.Errorf("app-server user-agent omitted version separator")
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || fields[0] == "" {
		return "", fmt.Errorf("app-server user-agent omitted version")
	}
	return fields[0], nil
}

func ProbeInfoFromInitializeResponse(response *InitializeResponse) (*ProbeInfo, error) {
	if response == nil {
		return nil, fmt.Errorf("initialize response is nil")
	}
	version, err := ParseVersionFromUserAgent(response.UserAgent)
	if err != nil {
		return nil, err
	}
	return &ProbeInfo{AppServerVersion: version}, nil
}

type ProbeInfo struct {
	AppServerVersion string `json:"appServerVersion"`
}

func ClassifyRemoteControlRPCMessage(requestID int64, raw json.RawMessage) (RemoteControlRPCResultKind, error) {
	var response struct {
		ID     *int64           `json:"id"`
		Result *json.RawMessage `json:"result"`
		Error  *struct {
			Code    int64  `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return RemoteControlRPCIgnored, err
	}
	if response.Method == "thread/status/changed" {
		return RemoteControlRPCIgnored, nil
	}
	if response.Method == "remoteControl/status/changed" {
		return RemoteControlRPCIgnored, nil
	}
	if response.ID == nil || *response.ID != requestID {
		return RemoteControlRPCIgnored, nil
	}
	if response.Error != nil {
		if response.Error.Code == InvalidParamsErrorCode {
			return RemoteControlRPCInvalidParams, nil
		}
		return RemoteControlRPCOtherError, errors.New(response.Error.Message)
	}
	return RemoteControlRPCSuccess, nil
}

func DecodeRemoteControlRPCMessage(requestID int64, raw json.RawMessage) (*RemoteControlRPCMessage, error) {
	var message struct {
		ID     *int64          `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int64  `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, err
	}
	if message.Method == "remoteControl/status/changed" {
		status, err := ReadyStatusFromRawNotification(message.Params)
		if err != nil {
			return nil, err
		}
		return &RemoteControlRPCMessage{Kind: RemoteControlMessageStatusNotification, Status: status}, nil
	}
	if message.ID == nil || *message.ID != requestID {
		return &RemoteControlRPCMessage{Kind: RemoteControlMessageIgnored}, nil
	}
	if message.Error != nil {
		return &RemoteControlRPCMessage{
			Kind:         RemoteControlMessageError,
			ErrorCode:    message.Error.Code,
			ErrorMessage: message.Error.Message,
		}, nil
	}
	return &RemoteControlRPCMessage{Kind: RemoteControlMessageResponse, Result: append(json.RawMessage(nil), message.Result...)}, nil
}

func ReadyStatusFromRawNotification(raw json.RawMessage) (*RemoteControlReadyStatus, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("remote control status notification omitted params")
	}
	var notification remotecontrol.StatusChangedNotification
	if err := json.Unmarshal(raw, &notification); err != nil {
		return nil, err
	}
	status := ReadyStatusFromNotification(&notification)
	return &status, nil
}

func UpdateLatestReadyStatus(latest *RemoteControlReadyStatus, notification *RemoteControlReadyStatus) *RemoteControlReadyStatus {
	if notification == nil {
		if latest == nil {
			return nil
		}
		clone := *latest
		clone.EnvironmentID = cloneString(latest.EnvironmentID)
		return &clone
	}
	clone := *notification
	clone.EnvironmentID = cloneString(notification.EnvironmentID)
	return &clone
}

func EnableRemoteControlOnSocket(socketPath string, connectTimeout time.Duration, connectRetryDelay time.Duration) (RemoteControlReadyStatus, error) {
	if runtime.GOOS == "windows" {
		return RemoteControlReadyStatus{}, ErrUnsupportedPlatform
	}
	conn, err := connectUnixSocketWithRetry(socketPath, connectTimeout, connectRetryDelay)
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	defer conn.Close()
	client := newLocalSocketRemoteControlClient(conn)
	return client.enableRemoteControl()
}

func DisableRemoteControlOnSocket(socketPath string, connectTimeout time.Duration, connectRetryDelay time.Duration) (RemoteControlReadyStatus, error) {
	if runtime.GOOS == "windows" {
		return RemoteControlReadyStatus{}, ErrUnsupportedPlatform
	}
	conn, err := connectUnixSocketWithRetry(socketPath, connectTimeout, connectRetryDelay)
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	defer conn.Close()
	client := newLocalSocketRemoteControlClient(conn)
	return client.disableRemoteControl()
}

func ProbeAppServerVersionOnSocket(socketPath string, timeout time.Duration) (string, error) {
	if runtime.GOOS == "windows" {
		return "", ErrUnsupportedPlatform
	}
	conn, err := connectUnixSocketWithRetry(socketPath, timeout, 25*time.Millisecond)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	client := newLocalSocketRemoteControlClient(conn)
	if err := conn.SetDeadline(time.Now().Add(defaultDuration(timeout, ControlSocketProbeTimeout))); err != nil {
		return "", err
	}
	response, err := client.initialize()
	if err != nil {
		return "", err
	}
	return ParseVersionFromUserAgent(response.UserAgent)
}

type localSocketRemoteControlClient struct {
	conn    net.Conn
	scanner *bufio.Scanner
	encoder *json.Encoder
}

func newLocalSocketRemoteControlClient(conn net.Conn) *localSocketRemoteControlClient {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	return &localSocketRemoteControlClient{
		conn:    conn,
		scanner: scanner,
		encoder: json.NewEncoder(conn),
	}
}

func connectUnixSocketWithRetry(socketPath string, connectTimeout time.Duration, connectRetryDelay time.Duration) (net.Conn, error) {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return nil, fmt.Errorf("%w: socket path is empty", ErrDaemonPathsRequired)
	}
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}
	if connectRetryDelay <= 0 {
		connectRetryDelay = 50 * time.Millisecond
	}
	deadline := time.Now().Add(connectTimeout)
	var lastErr error
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if lastErr != nil {
				return nil, fmt.Errorf("app server did not become ready on %s: %w", socketPath, lastErr)
			}
			return nil, fmt.Errorf("app server did not become ready on %s", socketPath)
		}
		conn, err := net.DialTimeout("unix", socketPath, minDuration(connectRetryDelay, remaining))
		if err == nil {
			return conn, nil
		}
		lastErr = err
		time.Sleep(minDuration(connectRetryDelay, time.Until(deadline)))
	}
}

func (c *localSocketRemoteControlClient) enableRemoteControl() (RemoteControlReadyStatus, error) {
	if c == nil || c.conn == nil {
		return RemoteControlReadyStatus{}, fmt.Errorf("%w: remote-control socket client is nil", ErrDaemonPathsRequired)
	}
	if err := c.conn.SetDeadline(time.Now().Add(RemoteControlReadyTimeout)); err != nil {
		return RemoteControlReadyStatus{}, err
	}
	if _, err := c.initialize(); err != nil {
		return RemoteControlReadyStatus{}, err
	}
	status, err := c.requestEnableWithFallback()
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	if status.Status == remotecontrol.StatusConnecting {
		status, err = c.waitForRemoteControlStatus(status, RemoteControlReadyTimeout)
		if err != nil {
			return RemoteControlReadyStatus{}, err
		}
	}
	_ = c.conn.SetDeadline(time.Time{})
	return status, nil
}

func (c *localSocketRemoteControlClient) disableRemoteControl() (RemoteControlReadyStatus, error) {
	if c == nil || c.conn == nil {
		return RemoteControlReadyStatus{}, fmt.Errorf("%w: remote-control socket client is nil", ErrDaemonPathsRequired)
	}
	if err := c.conn.SetDeadline(time.Now().Add(RemoteControlReadyTimeout)); err != nil {
		return RemoteControlReadyStatus{}, err
	}
	if _, err := c.initialize(); err != nil {
		return RemoteControlReadyStatus{}, err
	}
	status, err := c.requestDisableWithFallback()
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	_ = c.conn.SetDeadline(time.Time{})
	return status, nil
}

func (c *localSocketRemoteControlClient) initialize() (*InitializeResponse, error) {
	if err := c.encoder.Encode(BuildInitializeRequest("0.0.0", true)); err != nil {
		return nil, fmt.Errorf("failed to send initialize request: %w", err)
	}
	response, err := c.readInitializeResponse()
	if err != nil {
		return nil, err
	}
	if err := c.encoder.Encode(InitializedNotification()); err != nil {
		return nil, fmt.Errorf("failed to send initialized notification: %w", err)
	}
	return response, nil
}

func (c *localSocketRemoteControlClient) readInitializeResponse() (*InitializeResponse, error) {
	for c.scanner.Scan() {
		raw := append(json.RawMessage(nil), c.scanner.Bytes()...)
		var response struct {
			ID     *int64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, err
		}
		if response.ID == nil || *response.ID != InitializeRequestID {
			continue
		}
		if response.Error != nil {
			return nil, errors.New(response.Error.Message)
		}
		var initialize InitializeResponse
		if len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, &initialize); err != nil {
				return nil, err
			}
		}
		return &initialize, nil
	}
	if err := c.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (c *localSocketRemoteControlClient) requestEnableWithFallback() (RemoteControlReadyStatus, error) {
	status, err := c.requestEnable(remotecontrol.EnableParams{Ephemeral: true})
	if errors.Is(err, errRemoteControlInvalidParams) {
		status, err = c.requestEnable(nil)
	}
	return status, err
}

func (c *localSocketRemoteControlClient) requestEnable(params any) (RemoteControlReadyStatus, error) {
	if err := c.encoder.Encode(BuildRemoteControlRequest("remoteControl/enable", params)); err != nil {
		return RemoteControlReadyStatus{}, fmt.Errorf("failed to send remoteControl/enable request: %w", err)
	}
	result, err := c.readRemoteControlResponse("remoteControl/enable")
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	var response remotecontrol.EnableResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return RemoteControlReadyStatus{}, fmt.Errorf("failed to parse remoteControl/enable response: %w", err)
	}
	return ReadyStatusFromEnable(&response), nil
}

func (c *localSocketRemoteControlClient) requestDisableWithFallback() (RemoteControlReadyStatus, error) {
	status, err := c.requestDisable(remotecontrol.DisableParams{Ephemeral: true})
	if errors.Is(err, errRemoteControlInvalidParams) {
		status, err = c.requestDisable(nil)
	}
	return status, err
}

func (c *localSocketRemoteControlClient) requestDisable(params any) (RemoteControlReadyStatus, error) {
	if err := c.encoder.Encode(BuildRemoteControlRequest("remoteControl/disable", params)); err != nil {
		return RemoteControlReadyStatus{}, fmt.Errorf("failed to send remoteControl/disable request: %w", err)
	}
	result, err := c.readRemoteControlResponse("remoteControl/disable")
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	var response remotecontrol.DisableResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return RemoteControlReadyStatus{}, fmt.Errorf("failed to parse remoteControl/disable response: %w", err)
	}
	return ReadyStatusFromDisable(&response), nil
}

func (c *localSocketRemoteControlClient) readRemoteControlResponse(method string) (json.RawMessage, error) {
	for c.scanner.Scan() {
		raw := append(json.RawMessage(nil), c.scanner.Bytes()...)
		message, err := DecodeRemoteControlRPCMessage(RemoteControlRequestID, raw)
		if err != nil {
			return nil, err
		}
		switch message.Kind {
		case RemoteControlMessageResponse:
			return append(json.RawMessage(nil), message.Result...), nil
		case RemoteControlMessageError:
			if message.ErrorCode == InvalidParamsErrorCode {
				return nil, errRemoteControlInvalidParams
			}
			return nil, fmt.Errorf("%s failed: %s", method, message.ErrorMessage)
		case RemoteControlMessageStatusNotification, RemoteControlMessageIgnored:
			continue
		}
	}
	if err := c.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func (c *localSocketRemoteControlClient) waitForRemoteControlStatus(latest RemoteControlReadyStatus, readyTimeout time.Duration) (RemoteControlReadyStatus, error) {
	if readyTimeout <= 0 {
		readyTimeout = RemoteControlReadyTimeout
	}
	deadline := time.Now().Add(readyTimeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			latest.TimedOut = true
			return latest, nil
		}
		if err := c.conn.SetReadDeadline(time.Now().Add(remaining)); err != nil {
			return RemoteControlReadyStatus{}, err
		}
		if !c.scanner.Scan() {
			if err := c.scanner.Err(); err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					latest.TimedOut = true
					return latest, nil
				}
				return RemoteControlReadyStatus{}, err
			}
			return latest, nil
		}
		message, err := DecodeRemoteControlRPCMessage(RemoteControlRequestID, append(json.RawMessage(nil), c.scanner.Bytes()...))
		if err != nil {
			return RemoteControlReadyStatus{}, err
		}
		if message.Kind != RemoteControlMessageStatusNotification || message.Status == nil {
			continue
		}
		latest = *message.Status
		if latest.Status != remotecontrol.StatusConnecting {
			return latest, nil
		}
	}
}

func minDuration(a time.Duration, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func defaultDuration(value time.Duration, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
