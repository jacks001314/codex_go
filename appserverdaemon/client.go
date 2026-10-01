package appserverdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"codex_go/remotecontrol"
)

const (
	ClientName                   = "codex_app_server_daemon"
	InitializeRequestID    int64 = 1
	RemoteControlRequestID int64 = 2
	InvalidParamsErrorCode       = -32602

	RemoteControlReadyTimeout = 10 * time.Second
	ControlSocketProbeTimeout = 200 * time.Millisecond
	// ControlSocketResponseTimeout bounds one control-socket exchange (Rust
	// client::CONTROL_SOCKET_RESPONSE_TIMEOUT).
	ControlSocketResponseTimeout = 2 * time.Second
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
	conn, err := connectUnixSocketWithRetry(socketPath, connectTimeout, connectRetryDelay)
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	client := newLocalSocketRemoteControlClient(conn)
	return client.enableRemoteControl()
}

func DisableRemoteControlOnSocket(socketPath string, connectTimeout time.Duration, connectRetryDelay time.Duration) (RemoteControlReadyStatus, error) {
	conn, err := connectUnixSocketWithRetry(socketPath, connectTimeout, connectRetryDelay)
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	client := newLocalSocketRemoteControlClient(conn)
	return client.disableRemoteControl()
}

func ProbeAppServerVersionOnSocket(socketPath string, timeout time.Duration) (string, error) {
	conn, err := connectUnixSocketWithRetry(socketPath, timeout, 25*time.Millisecond)
	if err != nil {
		return "", err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	client := newLocalSocketRemoteControlClient(conn)
	client.setDeadline(time.Now().Add(defaultDuration(timeout, ControlSocketProbeTimeout)))
	response, err := client.initialize()
	if err != nil {
		return "", err
	}
	return ParseVersionFromUserAgent(response.UserAgent)
}

// localSocketRemoteControlClient speaks the control socket's WebSocket
// transport (Rust app-server-client::connect_unix_socket_endpoint): every
// request is one text frame and every answer is another.
type localSocketRemoteControlClient struct {
	conn *websocket.Conn
	// deadline bounds each read; the zero value uses the default response
	// timeout.
	deadline time.Time
}

func newLocalSocketRemoteControlClient(conn *websocket.Conn) *localSocketRemoteControlClient {
	return &localSocketRemoteControlClient{conn: conn}
}

// setDeadline replaces the client's read deadline.
func (c *localSocketRemoteControlClient) setDeadline(deadline time.Time) {
	if c == nil {
		return
	}
	c.deadline = deadline
}

// errControlSocketTimeout reports that a control-socket read exhausted its
// deadline, which callers treat like a network timeout.
var errControlSocketTimeout = errors.New("timed out waiting for the app-server control socket")

// errSocketPeerRejected marks a fatal peer-policy refusal, which the connect
// retry loop must not retry.
type errSocketPeerRejected struct{ err error }

func (e *errSocketPeerRejected) Error() string { return e.err.Error() }
func (e *errSocketPeerRejected) Unwrap() error { return e.err }

func (c *localSocketRemoteControlClient) sendMessage(payload any) error {
	if c == nil || c.conn == nil {
		return fmt.Errorf("%w: remote-control socket client is nil", ErrDaemonPathsRequired)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), ControlSocketResponseTimeout)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, encoded)
}

func (c *localSocketRemoteControlClient) readMessage() (json.RawMessage, error) {
	if c == nil || c.conn == nil {
		return nil, fmt.Errorf("%w: remote-control socket client is nil", ErrDaemonPathsRequired)
	}
	timeout := ControlSocketResponseTimeout
	if !c.deadline.IsZero() {
		remaining := time.Until(c.deadline)
		if remaining <= 0 {
			return nil, errControlSocketTimeout
		}
		timeout = remaining
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	messageType, data, err := c.conn.Read(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, errControlSocketTimeout
		}
		return nil, err
	}
	if messageType != websocket.MessageText {
		return nil, errors.New("app-server control socket sent a non-text frame")
	}
	return json.RawMessage(append([]byte(nil), data...)), nil
}

// connectUnixSocketWithRetry dials the control socket, verifies the peer, and
// upgrades the connection to the WebSocket transport Rust's client uses.
func connectUnixSocketWithRetry(socketPath string, connectTimeout time.Duration, connectRetryDelay time.Duration) (*websocket.Conn, error) {
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
	dialPath, release, err := prepareSocketDialPath(socketPath)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
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
		conn, err := dialUnixSocketWebSocket(dialPath, minDuration(connectRetryDelay, remaining))
		if err == nil {
			return conn, nil
		}
		// A peer that is another user or an elevated token must never receive
		// application data, so that failure is fatal rather than retried.
		var peerErr *errSocketPeerRejected
		if errors.As(err, &peerErr) {
			return nil, fmt.Errorf("refusing the app-server connection on %s: %w", socketPath, peerErr)
		}
		lastErr = err
		time.Sleep(minDuration(connectRetryDelay, time.Until(deadline)))
	}
}

// dialUnixSocketWebSocket upgrades a peer-checked unix connection to the
// control socket's WebSocket protocol (Rust UDS_WEBSOCKET_HANDSHAKE_URL).
func dialUnixSocketWebSocket(dialPath string, timeout time.Duration) (*websocket.Conn, error) {
	return dialUnixSocketWebSocketURL(dialPath, UDSWebSocketHandshakeURL, timeout)
}

// dialUnixSocketWebSocketURL upgrades a peer-checked unix connection to the
// WebSocket route at handshakeURL.
func dialUnixSocketWebSocketURL(dialPath string, handshakeURL string, timeout time.Duration) (*websocket.Conn, error) {
	if timeout <= 0 {
		timeout = ControlSocketResponseTimeout
	}
	var peerErr error
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, "unix", dialPath)
			if err != nil {
				return nil, err
			}
			if err := ensureSocketPeerAllowed(conn); err != nil {
				_ = conn.Close()
				peerErr = err
				return nil, err
			}
			return conn, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, handshakeURL, &websocket.DialOptions{
		HTTPClient: &http.Client{Transport: transport},
	})
	if err != nil {
		if peerErr != nil {
			return nil, &errSocketPeerRejected{err: peerErr}
		}
		return nil, err
	}
	if max := RemoteAppServerMaxWebSocketMessageSize; max > 0 {
		conn.SetReadLimit(int64(max))
	}
	return conn, nil
}

// RequestDaemonShutdown asks a managed app-server to stop through its control
// socket (Rust app-server-daemon client::request_shutdown): connect to the
// `/daemon/shutdown` route, send this process's PID, and require the same PID
// echoed back as acknowledgment.
func RequestDaemonShutdown(socketPath string, pid uint32, timeout time.Duration) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return fmt.Errorf("%w: socket path is empty", ErrDaemonPathsRequired)
	}
	if timeout <= 0 {
		timeout = ControlSocketResponseTimeout
	}
	dialPath, release, err := prepareSocketDialPath(socketPath)
	if err != nil {
		return err
	}
	if release != nil {
		defer release()
	}
	conn, err := dialUnixSocketWebSocketURL(dialPath, UDSDaemonShutdownHandshakeURL, timeout)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", socketPath, err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	want := strconv.FormatUint(uint64(pid), 10)
	if err := conn.Write(ctx, websocket.MessageText, []byte(want)); err != nil {
		return fmt.Errorf("failed to request managed app-server shutdown: %w", err)
	}
	messageType, data, err := conn.Read(ctx)
	if err != nil {
		return fmt.Errorf("shutdown socket closed without acknowledgment: %w", err)
	}
	if messageType != websocket.MessageText || string(data) != want {
		return fmt.Errorf("shutdown acknowledgment did not match the managed process %d", pid)
	}
	return nil
}

func (c *localSocketRemoteControlClient) enableRemoteControl() (RemoteControlReadyStatus, error) {
	if c == nil || c.conn == nil {
		return RemoteControlReadyStatus{}, fmt.Errorf("%w: remote-control socket client is nil", ErrDaemonPathsRequired)
	}
	c.setDeadline(time.Now().Add(RemoteControlReadyTimeout))
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
	c.setDeadline(time.Time{})
	return status, nil
}

func (c *localSocketRemoteControlClient) disableRemoteControl() (RemoteControlReadyStatus, error) {
	if c == nil || c.conn == nil {
		return RemoteControlReadyStatus{}, fmt.Errorf("%w: remote-control socket client is nil", ErrDaemonPathsRequired)
	}
	c.setDeadline(time.Now().Add(RemoteControlReadyTimeout))
	if _, err := c.initialize(); err != nil {
		return RemoteControlReadyStatus{}, err
	}
	status, err := c.requestDisableWithFallback()
	if err != nil {
		return RemoteControlReadyStatus{}, err
	}
	c.setDeadline(time.Time{})
	return status, nil
}

func (c *localSocketRemoteControlClient) initialize() (*InitializeResponse, error) {
	if err := c.sendMessage(BuildInitializeRequest("0.0.0", true)); err != nil {
		return nil, fmt.Errorf("failed to send initialize request: %w", err)
	}
	response, err := c.readInitializeResponse()
	if err != nil {
		return nil, err
	}
	if err := c.sendMessage(InitializedNotification()); err != nil {
		return nil, fmt.Errorf("failed to send initialized notification: %w", err)
	}
	return response, nil
}

func (c *localSocketRemoteControlClient) readInitializeResponse() (*InitializeResponse, error) {
	for {
		raw, err := c.readMessage()
		if err != nil {
			return nil, err
		}
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
}

func (c *localSocketRemoteControlClient) requestEnableWithFallback() (RemoteControlReadyStatus, error) {
	status, err := c.requestEnable(remotecontrol.EnableParams{Ephemeral: true})
	if errors.Is(err, errRemoteControlInvalidParams) {
		status, err = c.requestEnable(nil)
	}
	return status, err
}

func (c *localSocketRemoteControlClient) requestEnable(params any) (RemoteControlReadyStatus, error) {
	if err := c.sendMessage(BuildRemoteControlRequest("remoteControl/enable", params)); err != nil {
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
	if err := c.sendMessage(BuildRemoteControlRequest("remoteControl/disable", params)); err != nil {
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
	for {
		raw, err := c.readMessage()
		if err != nil {
			return nil, err
		}
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
		c.setDeadline(time.Now().Add(remaining))
		raw, err := c.readMessage()
		if err != nil {
			if errors.Is(err, errControlSocketTimeout) {
				latest.TimedOut = true
				return latest, nil
			}
			return RemoteControlReadyStatus{}, err
		}
		message, err := DecodeRemoteControlRPCMessage(RemoteControlRequestID, raw)
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
