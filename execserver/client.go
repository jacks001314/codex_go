package execserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"codex_go/network"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type Client struct {
	conn           clientConnection
	url            string
	clientName     string
	sessionID      string
	open           clientConnectionOpener
	cleanup        func()
	writeMu        sync.Mutex
	recoverMu      sync.Mutex
	acceptedMu     sync.Mutex
	mu             sync.Mutex
	nextID         int64
	nextHTTPID     uint64
	pending        map[int64]chan clientCallResult
	sessions       map[string]*clientProcessSession
	httpStreams    map[string]*HTTPBodyStream
	inboundIDs     map[int64]struct{}
	inboundSlots   chan struct{}
	done           chan struct{}
	closeOnce      sync.Once
	closed         bool
	metadataMu     sync.Mutex
	metadata       map[string]*inFlightMetadataRequest
	cacheMu        sync.Mutex
	discoveryCache *CapabilityDiscoveryCache
	accepted       bool
}

type inFlightMetadataRequest struct {
	done      chan struct{}
	response  *FSGetMetadataResponse
	err       error
	abandoned bool
}

type clientConnection interface {
	Read(context.Context) ([]byte, error)
	Write(context.Context, []byte) error
	Close() error
	CloseNow() error
}

type clientConnectionOpener func(
	context.Context,
	string,
	func(string, json.RawMessage) error,
) (clientConnection, *InitializeResponse, error)

type websocketClientConnection struct {
	conn            *websocket.Conn
	keepaliveCancel context.CancelFunc
	closeOnce       sync.Once
	closeErr        error
}

func newWebSocketClientConnection(conn *websocket.Conn) *websocketClientConnection {
	return &websocketClientConnection{conn: conn, keepaliveCancel: startClientWebSocketKeepalive(conn)}
}

func (c *websocketClientConnection) Read(ctx context.Context) ([]byte, error) {
	_, data, err := c.conn.Read(ctx)
	return data, err
}

func (c *websocketClientConnection) Write(ctx context.Context, data []byte) error {
	return c.conn.Write(ctx, websocket.MessageText, data)
}

func (c *websocketClientConnection) Close() error {
	c.closeOnce.Do(func() {
		c.keepaliveCancel()
		c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
	})
	return c.closeErr
}

func (c *websocketClientConnection) CloseNow() error {
	c.closeOnce.Do(func() {
		c.keepaliveCancel()
		c.closeErr = c.conn.CloseNow()
	})
	return c.closeErr
}

func startClientWebSocketKeepalive(conn *websocket.Conn) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(remoteRelayKeepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					_ = conn.CloseNow()
					return
				}
			}
		}
	}()
	return cancel
}

var (
	clientRecoveryTimeout = 25 * time.Second
	clientRecoveryRetry   = 100 * time.Millisecond
)

type DialClientOptions struct {
	ClientName      string
	ResumeSessionID string
	HTTPClient      *http.Client
}

type ProcessEventKind string

const (
	ProcessEventOutput ProcessEventKind = "output"
	ProcessEventExited ProcessEventKind = "exited"
	ProcessEventClosed ProcessEventKind = "closed"
	ProcessEventLagged ProcessEventKind = "lagged"
)

type ProcessEvent struct {
	Kind          ProcessEventKind
	ProcessID     string
	Seq           uint64
	Stream        string
	Chunk         string
	ExitCode      int
	SandboxDenied *bool
}

type ProcessEventSubscription struct {
	client    *Client
	processID string
	session   *clientProcessSession
	mu        sync.Mutex
	queue     []ProcessEvent
	lagged    bool
	closed    bool
	err       error
	notify    chan struct{}
}

type clientProcessSession struct {
	mu            sync.Mutex
	lastPublished uint64
	pending       map[uint64]ProcessEvent
	subscription  *ProcessEventSubscription
	closed        bool
	policyMu      sync.Mutex
	policy        *networkPolicyDecisionController
	policyDone    chan struct{}
	policyOnce    sync.Once
}

type networkPolicyDecisionController struct {
	decider network.ProxyPolicyDecider
	timeout time.Duration
}

type HTTPBodyStream struct {
	client     *Client
	requestID  string
	mu         sync.Mutex
	queue      []HTTPRequestBodyDeltaNotification
	nextSeq    uint64
	pendingEOF bool
	closed     bool
	err        error
	notify     chan struct{}
}

type FileReadStream struct {
	client     *Client
	handleID   string
	mu         sync.Mutex
	offset     uint64
	pendingEOF bool
	closed     bool
}

type clientCallResult struct {
	response clientResponse
	err      error
}

type clientTransportError struct {
	err error
}

// IsRetryableRecoveryError reports whether an exec-server error should be
// retried after a transport disconnect and recovery (Rust #38420).
func IsRetryableRecoveryError(err error) bool {
	if err == nil {
		return false
	}
	var transportErr *clientTransportError
	return errors.As(err, &transportErr)
}

func (e *clientTransportError) Error() string {
	if e == nil || e.err == nil {
		return "exec-server transport disconnected"
	}
	return e.err.Error()
}

func (e *clientTransportError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type clientResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func DialClient(ctx context.Context, url string, clientName string) (*Client, error) {
	return DialClientWithOptions(ctx, url, DialClientOptions{ClientName: clientName})
}

func DialClientWithOptions(ctx context.Context, url string, options DialClientOptions) (*Client, error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, errors.New("exec-server URL is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	clientName := strings.TrimSpace(options.ClientName)
	if clientName == "" {
		clientName = "codex-go-unified-exec"
	}
	client := &Client{
		url:          url,
		clientName:   clientName,
		nextID:       1,
		nextHTTPID:   1,
		pending:      map[int64]chan clientCallResult{},
		sessions:     map[string]*clientProcessSession{},
		httpStreams:  map[string]*HTTPBodyStream{},
		inboundIDs:   map[int64]struct{}{},
		inboundSlots: make(chan struct{}, MaxInFlightServerRequests),
		done:         make(chan struct{}),
	}
	client.open = func(ctx context.Context, resumeSessionID string, handleNotification func(string, json.RawMessage) error) (clientConnection, *InitializeResponse, error) {
		return dialInitializedClientConnection(ctx, url, clientName, resumeSessionID, handleNotification, options.HTTPClient)
	}
	conn, initialized, err := client.open(ctx, options.ResumeSessionID, client.handleNotification)
	if err != nil {
		return nil, err
	}
	client.conn = conn
	client.sessionID = initialized.SessionID
	go client.readLoop(conn)
	return client, nil
}

func dialInitializedClientConnection(
	ctx context.Context,
	serverURL string,
	clientName string,
	resumeSessionID string,
	handleNotification func(string, json.RawMessage) error,
	httpClient *http.Client,
) (clientConnection, *InitializeResponse, error) {
	dialOptions := &websocket.DialOptions{HTTPClient: httpClient}
	conn, _, err := websocket.Dial(ctx, serverURL, dialOptions)
	if err != nil {
		return nil, nil, err
	}
	conn.SetReadLimit(16 * 1024 * 1024)
	var wire clientConnection
	if isRendezvousHarnessURL(serverURL) {
		wire, err = newRelayHarnessClientConnection(ctx, conn)
		if err != nil {
			_ = conn.CloseNow()
			return nil, nil, err
		}
	} else {
		wire = newWebSocketClientConnection(conn)
	}
	initialized, err := initializeClientConnection(ctx, wire, clientName, resumeSessionID, handleNotification)
	if err != nil {
		_ = wire.CloseNow()
		return nil, nil, err
	}
	return wire, initialized, nil
}

func initializeClientConnection(
	ctx context.Context,
	conn clientConnection,
	clientName string,
	resumeSessionID string,
	handleNotification func(string, json.RawMessage) error,
) (*InitializeResponse, error) {
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = conn.CloseNow()
		}
	}()
	params := InitializeParams{ClientName: clientName}
	if resumeSessionID != "" {
		params.ResumeSessionID = &resumeSessionID
	}
	requestData, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      int64(1),
		"method":  MethodInitialize,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	if err := conn.Write(ctx, requestData); err != nil {
		return nil, err
	}
	var initialized InitializeResponse
	for {
		data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return nil, err
		}
		if envelope.Method != "" {
			if handleNotification != nil {
				if err := handleNotification(envelope.Method, envelope.Params); err != nil {
					return nil, err
				}
			}
			continue
		}
		id, ok := clientResponseID(envelope.ID)
		if !ok || id != 1 {
			continue
		}
		var response clientResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return nil, err
		}
		if response.Error != nil {
			return nil, fmt.Errorf("exec-server %s failed (%d): %s", MethodInitialize, response.Error.Code, response.Error.Message)
		}
		if err := json.Unmarshal(response.Result, &initialized); err != nil {
			return nil, err
		}
		break
	}
	initializedData, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  MethodInitialized,
		"params":  map[string]any{},
	})
	if err != nil {
		return nil, err
	}
	if err := conn.Write(ctx, initializedData); err != nil {
		return nil, err
	}
	closeOnError = false
	return &initialized, nil
}

func (c *Client) SessionID() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

func (c *Client) HTTPRequest(ctx context.Context, params *HTTPRequestParams) (*HTTPRequestResponse, *HTTPBodyStream, error) {
	if params == nil {
		return nil, nil, errors.New("http/request params are required")
	}
	request := *params
	request.Headers = append([]HTTPHeader(nil), params.Headers...)
	if request.Headers == nil {
		request.Headers = []HTTPHeader{}
	}
	if request.RedirectPolicy == "" {
		request.RedirectPolicy = "follow"
	}
	var stream *HTTPBodyStream
	if request.StreamResponse {
		c.mu.Lock()
		select {
		case <-c.done:
			c.mu.Unlock()
			return nil, nil, errors.New("exec-server client is closed")
		default:
		}
		request.RequestID = fmt.Sprintf("http-%d", c.nextHTTPID)
		c.nextHTTPID++
		stream = &HTTPBodyStream{client: c, requestID: request.RequestID, nextSeq: 1, notify: make(chan struct{}, 1)}
		c.httpStreams[request.RequestID] = stream
		c.mu.Unlock()
	}
	var response HTTPRequestResponse
	if err := c.call(ctx, MethodHTTPRequest, &request, &response); err != nil {
		if stream != nil {
			stream.Close()
		}
		return nil, nil, err
	}
	return &response, stream, nil
}

func (s *HTTPBodyStream) Next(ctx context.Context) ([]byte, bool, error) {
	if s == nil {
		return nil, true, errors.New("http response stream is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		if s.pendingEOF {
			s.pendingEOF = false
			s.closed = true
			s.mu.Unlock()
			s.removeRoute()
			return nil, true, nil
		}
		if len(s.queue) > 0 {
			delta := s.queue[0]
			s.queue = s.queue[1:]
			if delta.Seq != s.nextSeq {
				expected := s.nextSeq
				s.closed = true
				s.mu.Unlock()
				s.removeRoute()
				return nil, true, fmt.Errorf("http response stream %q received seq %d, expected %d", s.requestID, delta.Seq, expected)
			}
			s.nextSeq++
			data, err := base64.StdEncoding.DecodeString(delta.DeltaBase64)
			if err != nil {
				s.closed = true
				s.mu.Unlock()
				s.removeRoute()
				return nil, true, err
			}
			if delta.Error != nil {
				s.closed = true
				s.mu.Unlock()
				s.removeRoute()
				return nil, true, fmt.Errorf("http response stream %q failed: %s", s.requestID, *delta.Error)
			}
			if delta.Done {
				s.closed = true
				if len(data) > 0 {
					s.pendingEOF = true
				}
			}
			done := delta.Done && len(data) == 0
			s.mu.Unlock()
			if delta.Done {
				s.removeRoute()
			}
			return data, done, nil
		}
		if s.closed {
			err := s.err
			s.mu.Unlock()
			if err != nil {
				return nil, true, err
			}
			return nil, true, nil
		}
		s.mu.Unlock()
		select {
		case <-s.notify:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
}

func (s *HTTPBodyStream) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.removeRoute()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *HTTPBodyStream) publish(delta HTTPRequestBodyDeltaNotification) {
	const bodyDeltaCapacity = 256
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if len(s.queue) >= bodyDeltaCapacity {
		s.closed = true
		s.err = fmt.Errorf("http response stream %q failed: body delta channel filled before delivery", s.requestID)
		s.mu.Unlock()
		s.removeRoute()
		select {
		case s.notify <- struct{}{}:
		default:
		}
		return
	}
	s.queue = append(s.queue, delta)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *HTTPBodyStream) fail(err error) {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.err = err
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *HTTPBodyStream) removeRoute() {
	if s == nil || s.client == nil {
		return
	}
	s.client.mu.Lock()
	if s.client.httpStreams[s.requestID] == s {
		delete(s.client.httpStreams, s.requestID)
	}
	s.client.mu.Unlock()
}

func (c *Client) SubscribeProcessEvents(processID string) (*ProcessEventSubscription, error) {
	if c == nil {
		return nil, errors.New("exec-server client is closed")
	}
	processID = strings.TrimSpace(processID)
	if processID == "" {
		return nil, errors.New("process id is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.conn == nil {
		return nil, errors.New("exec-server client is closed")
	}
	select {
	case <-c.done:
		return nil, errors.New("exec-server client is closed")
	default:
	}
	if _, exists := c.sessions[processID]; exists {
		return nil, fmt.Errorf("process event subscription already exists for %s", processID)
	}
	session := &clientProcessSession{pending: map[uint64]ProcessEvent{}, policyDone: make(chan struct{})}
	subscription := &ProcessEventSubscription{
		client:    c,
		processID: processID,
		session:   session,
		notify:    make(chan struct{}, 1),
	}
	session.subscription = subscription
	c.sessions[processID] = session
	return subscription, nil
}

func (s *ProcessEventSubscription) Next(ctx context.Context) (ProcessEvent, error) {
	if s == nil {
		return ProcessEvent{}, errors.New("process event subscription is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		if s.lagged {
			s.lagged = false
			s.mu.Unlock()
			return ProcessEvent{Kind: ProcessEventLagged, ProcessID: s.processID}, nil
		}
		if len(s.queue) > 0 {
			event := s.queue[0]
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return event, nil
		}
		if s.closed {
			err := s.err
			s.mu.Unlock()
			if err == nil {
				err = errors.New("process event subscription is closed")
			}
			return ProcessEvent{}, err
		}
		s.mu.Unlock()
		select {
		case <-s.notify:
		case <-ctx.Done():
			return ProcessEvent{}, ctx.Err()
		}
	}
}

func (s *ProcessEventSubscription) Close() {
	if s == nil {
		return
	}
	if s.client != nil {
		s.client.mu.Lock()
		if s.client.sessions[s.processID] == s.session {
			delete(s.client.sessions, s.processID)
		}
		s.client.mu.Unlock()
	}
	s.session.cancelNetworkPolicy()
	s.close(errors.New("process event subscription is closed"))
}

func (c *Client) RegisterNetworkPolicyController(processID string, timeout time.Duration, decider network.ProxyPolicyDecider) error {
	if c == nil {
		return errors.New("exec-server client is closed")
	}
	processID = strings.TrimSpace(processID)
	if processID == "" || len(processID) > MaxNetworkPolicyProcessIDBytes {
		return fmt.Errorf("process id must be 1..%d bytes", MaxNetworkPolicyProcessIDBytes)
	}
	if timeout <= 0 {
		return errors.New("network policy decision timeout must be positive")
	}
	if decider == nil {
		return errors.New("network policy decider is required")
	}
	c.mu.Lock()
	session := c.sessions[processID]
	c.mu.Unlock()
	if session == nil {
		return fmt.Errorf("unknown process id %s", processID)
	}
	session.policyMu.Lock()
	defer session.policyMu.Unlock()
	select {
	case <-session.policyDone:
		return fmt.Errorf("process id %s is closed", processID)
	default:
	}
	session.policy = &networkPolicyDecisionController{decider: decider, timeout: timeout}
	return nil
}

func (s *clientProcessSession) networkPolicySnapshot() (*networkPolicyDecisionController, <-chan struct{}) {
	if s == nil {
		return nil, nil
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	return s.policy, s.policyDone
}

func (s *clientProcessSession) cancelNetworkPolicy() {
	if s == nil {
		return
	}
	s.policyOnce.Do(func() {
		s.policyMu.Lock()
		s.policy = nil
		close(s.policyDone)
		s.policyMu.Unlock()
	})
}

func (s *ProcessEventSubscription) publish(event ProcessEvent) {
	const eventCapacity = 256
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if len(s.queue) == eventCapacity {
		copy(s.queue, s.queue[1:])
		s.queue = s.queue[:eventCapacity-1]
		s.lagged = true
	}
	s.queue = append(s.queue, event)
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *ProcessEventSubscription) close(err error) {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.err = err
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (c *Client) Start(ctx context.Context, params *ExecParams) (*ExecResponse, error) {
	if params == nil {
		return nil, errors.New("process/start params are required")
	}
	request := *params
	cwd, err := normalizeExecCWDForWire(params.CWD)
	if err != nil {
		return nil, err
	}
	request.CWD = cwd
	var response ExecResponse
	if err := c.call(ctx, MethodProcessStart, &request, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Read(ctx context.Context, params *ReadParams) (*ReadResponse, error) {
	var response ReadResponse
	if err := c.call(ctx, MethodProcessRead, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Write(ctx context.Context, params *WriteParams) (*WriteResponse, error) {
	var response WriteResponse
	if err := c.call(ctx, MethodProcessWrite, params, &response); err != nil {
		var transportErr *clientTransportError
		if !errors.As(err, &transportErr) {
			return nil, err
		}
		if err := c.ensureConnected(ctx); err != nil {
			return nil, err
		}
		if err := c.call(ctx, MethodProcessWrite, params, &response); err != nil {
			return nil, err
		}
	}
	return &response, nil
}

func (c *Client) Signal(ctx context.Context, params *SignalParams) (*SignalResponse, error) {
	var response SignalResponse
	if err := c.call(ctx, MethodProcessSignal, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) Terminate(ctx context.Context, params *TerminateParams) (*TerminateResponse, error) {
	var response TerminateResponse
	if err := c.call(ctx, MethodProcessTerminate, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) EnvironmentInfo(ctx context.Context) (*EnvironmentInfo, error) {
	var response EnvironmentInfo
	if err := c.call(ctx, MethodEnvironmentInfo, map[string]any{}, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) EnvironmentStatus(ctx context.Context) (*EnvironmentStatus, error) {
	var response EnvironmentStatus
	if err := c.call(ctx, MethodEnvironmentStatus, map[string]any{}, &response); err != nil {
		return nil, err
	}
	if response.Status == "" {
		response.Status = EnvironmentStatusReady
	}
	return &response, nil
}

func (c *Client) DiscoverCapabilityRoots(ctx context.Context, params *CapabilityRootsDiscoverParams) (*CapabilityRootsDiscoverResponse, error) {
	if cached, ok := c.cachedDiscovery(params); ok {
		return cached.response, cached.err
	}
	response, err := c.discoverCapabilityRoots(ctx, params)
	if err != nil && IsRetryableRecoveryError(err) {
		response, err = c.discoverCapabilityRoots(ctx, params)
	}
	c.cacheDiscovery(params, response, err, err != nil && IsRetryableRecoveryError(err))
	return response, err
}

func (c *Client) cachedDiscovery(params *CapabilityRootsDiscoverParams) (cachedDiscoveryEntry, bool) {
	if c == nil {
		return cachedDiscoveryEntry{}, false
	}
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if c.discoveryCache == nil {
		return cachedDiscoveryEntry{}, false
	}
	return c.discoveryCache.Get(params)
}

func (c *Client) cacheDiscovery(params *CapabilityRootsDiscoverParams, response *CapabilityRootsDiscoverResponse, err error, retryable bool) {
	if c == nil {
		return
	}
	c.cacheMu.Lock()
	if c.discoveryCache == nil {
		c.discoveryCache = NewCapabilityDiscoveryCache()
	}
	c.discoveryCache.Put(params, response, err, retryable)
	c.cacheMu.Unlock()
}

func (c *Client) discoverCapabilityRoots(ctx context.Context, params *CapabilityRootsDiscoverParams) (*CapabilityRootsDiscoverResponse, error) {
	response := CapabilityRootsDiscoverResponse{Roots: []CapabilityRootDiscovery{}}
	if params == nil || len(params.Roots) == 0 {
		return &response, nil
	}
	normalizedRoots := make([]CapabilityRootDiscoverRequest, len(params.Roots))
	for i := range params.Roots {
		normalizedRoots[i] = params.Roots[i]
		var err error
		normalizedRoots[i].Path, err = normalizeFSPathForWire(params.Roots[i].Path)
		if err != nil {
			return nil, fmt.Errorf("capability root %q path: %w", params.Roots[i].ID, err)
		}
		normalizedRoots[i].Sandbox, err = normalizeFSSandboxForWire(params.Roots[i].Sandbox)
		if err != nil {
			return nil, fmt.Errorf("capability root %q sandbox: %w", params.Roots[i].ID, err)
		}
	}
	for start := 0; start < len(normalizedRoots); start += maxCapabilityDiscoveryRoots {
		end := start + maxCapabilityDiscoveryRoots
		if end > len(normalizedRoots) {
			end = len(normalizedRoots)
		}
		batchParams := CapabilityRootsDiscoverParams{Roots: append([]CapabilityRootDiscoverRequest(nil), normalizedRoots[start:end]...)}
		var batch CapabilityRootsDiscoverResponse
		if err := c.call(ctx, MethodCapabilityRootsDiscover, &batchParams, &batch); err != nil {
			return nil, err
		}
		response.Roots = append(response.Roots, batch.Roots...)
	}
	return &response, nil
}

func (c *Client) DiscoverCapabilities(ctx context.Context, params *CapabilityDiscoveryParams) (*CapabilityDiscoveryResponse, error) {
	return c.DiscoverCapabilityRoots(ctx, params)
}

func (c *Client) FSReadFile(ctx context.Context, params *FSReadFileParams) (*FSReadFileResponse, error) {
	normalized, err := normalizeFSReadFileParams(params)
	if err != nil {
		return nil, err
	}
	var response FSReadFileResponse
	if err := c.call(ctx, MethodFSReadFile, normalized, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSOpen(ctx context.Context, params *FSOpenParams) (*FSOpenResponse, error) {
	if params == nil {
		return nil, errors.New("fs/open params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/open path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/open: %w", err)
	}
	var response FSOpenResponse
	if err := c.call(ctx, MethodFSOpen, &normalized, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSReadFileStream(ctx context.Context, params *FSReadFileParams) (*FileReadStream, error) {
	if params == nil {
		return nil, errors.New("fs/readFile params are required")
	}
	handleID := strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := c.FSOpen(ctx, &FSOpenParams{HandleID: handleID, Path: params.Path, Sandbox: params.Sandbox}); err != nil {
		return nil, err
	}
	return &FileReadStream{client: c, handleID: handleID}, nil
}

func (s *FileReadStream) Next(ctx context.Context) ([]byte, bool, error) {
	if s == nil || s.client == nil {
		return nil, true, errors.New("file read stream is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingEOF {
		s.pendingEOF = false
		s.closed = true
		return nil, true, nil
	}
	if s.closed {
		return nil, true, nil
	}
	response, err := s.client.FSReadBlock(ctx, &FSReadBlockParams{
		HandleID: s.handleID,
		Offset:   s.offset,
		Len:      fileReadChunkSize,
	})
	if err != nil {
		s.closed = true
		return nil, true, err
	}
	chunk, err := base64.StdEncoding.DecodeString(response.Chunk)
	if err != nil {
		s.closeLocked(ctx)
		return nil, true, fmt.Errorf("fs/readBlock returned invalid base64 chunk: %w", err)
	}
	if len(chunk) > fileReadChunkSize {
		s.closeLocked(ctx)
		return nil, true, fmt.Errorf("fs/readBlock returned %d bytes, maximum is %d", len(chunk), fileReadChunkSize)
	}
	if response.EOF {
		s.closeLocked(ctx)
		if len(chunk) == 0 {
			return nil, true, nil
		}
		s.pendingEOF = true
		s.closed = false
		return chunk, false, nil
	}
	if len(chunk) == 0 {
		s.closeLocked(ctx)
		return nil, true, errors.New("fs/readBlock returned an empty non-terminal block")
	}
	if uint64(len(chunk)) > ^uint64(0)-s.offset {
		s.closeLocked(ctx)
		return nil, true, fmt.Errorf("fs/readBlock offset overflowed after %d bytes", s.offset)
	}
	s.offset += uint64(len(chunk))
	return chunk, false, nil
}

func (s *FileReadStream) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed && !s.pendingEOF {
		return nil
	}
	return s.closeLocked(ctx)
}

func (s *FileReadStream) closeLocked(ctx context.Context) error {
	if s.closed && !s.pendingEOF {
		return nil
	}
	s.closed = true
	s.pendingEOF = false
	_, err := s.client.FSClose(ctx, &FSCloseParams{HandleID: s.handleID})
	return err
}

func (c *Client) FSReadBlock(ctx context.Context, params *FSReadBlockParams) (*FSReadBlockResponse, error) {
	var response FSReadBlockResponse
	if err := c.call(ctx, MethodFSReadBlock, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSClose(ctx context.Context, params *FSCloseParams) (*FSCloseResponse, error) {
	var response FSCloseResponse
	if err := c.call(ctx, MethodFSClose, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSWriteFile(ctx context.Context, params *FSWriteFileParams) (*FSWriteFileResponse, error) {
	if params == nil {
		return nil, errors.New("fs/writeFile params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/writeFile path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/writeFile: %w", err)
	}
	var response FSWriteFileResponse
	err = c.call(ctx, MethodFSWriteFile, &normalized, &response)
	c.clearInFlightMetadataRequests()
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSCreateDirectory(ctx context.Context, params *FSCreateDirectoryParams) (*FSCreateDirectoryResponse, error) {
	if params == nil {
		return nil, errors.New("fs/createDirectory params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/createDirectory path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/createDirectory: %w", err)
	}
	var response FSCreateDirectoryResponse
	err = c.call(ctx, MethodFSCreateDirectory, &normalized, &response)
	c.clearInFlightMetadataRequests()
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSGetMetadata(ctx context.Context, params *FSGetMetadataParams) (*FSGetMetadataResponse, error) {
	if params == nil {
		return nil, errors.New("fs/getMetadata params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/getMetadata path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/getMetadata: %w", err)
	}
	if normalized.Sandbox != nil {
		return c.fsGetMetadataUncoalesced(ctx, &normalized)
	}
	return c.fsGetMetadataCoalesced(ctx, &normalized)
}

func (c *Client) fsGetMetadataCoalesced(ctx context.Context, params *FSGetMetadataParams) (*FSGetMetadataResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		c.metadataMu.Lock()
		if c.metadata == nil {
			c.metadata = make(map[string]*inFlightMetadataRequest)
		}
		request := c.metadata[params.Path]
		owner := request == nil
		if owner {
			request = &inFlightMetadataRequest{done: make(chan struct{})}
			c.metadata[params.Path] = request
		}
		c.metadataMu.Unlock()

		if owner {
			response, err := c.fsGetMetadataUncoalesced(ctx, params)
			c.metadataMu.Lock()
			if c.metadata[params.Path] == request {
				delete(c.metadata, params.Path)
			}
			if err != nil && ctx.Err() != nil {
				request.abandoned = true
			} else {
				request.response = response
				request.err = err
			}
			close(request.done)
			c.metadataMu.Unlock()
			return response, err
		}

		select {
		case <-request.done:
			if request.abandoned {
				continue
			}
			if request.response == nil {
				return nil, request.err
			}
			response := *request.response
			return &response, request.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) fsGetMetadataUncoalesced(ctx context.Context, params *FSGetMetadataParams) (*FSGetMetadataResponse, error) {
	var response FSGetMetadataResponse
	if err := c.call(ctx, MethodFSGetMetadata, params, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) clearInFlightMetadataRequests() {
	if c == nil {
		return
	}
	c.metadataMu.Lock()
	clear(c.metadata)
	c.metadataMu.Unlock()
}

func (c *Client) FSCanonicalize(ctx context.Context, params *FSCanonicalizeParams) (*FSCanonicalizeResponse, error) {
	if params == nil {
		return nil, errors.New("fs/canonicalize params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/canonicalize path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/canonicalize: %w", err)
	}
	var response FSCanonicalizeResponse
	if err := c.call(ctx, MethodFSCanonicalize, &normalized, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSReadDirectory(ctx context.Context, params *FSReadDirectoryParams) (*FSReadDirectoryResponse, error) {
	if params == nil {
		return nil, errors.New("fs/readDirectory params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/readDirectory path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/readDirectory: %w", err)
	}
	var response FSReadDirectoryResponse
	if err := c.call(ctx, MethodFSReadDirectory, &normalized, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSWalk(ctx context.Context, params *FSWalkParams) (*FSWalkResponse, error) {
	if params == nil {
		return nil, errors.New("fs/walk params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/walk path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/walk: %w", err)
	}
	var response FSWalkResponse
	if err := c.call(ctx, MethodFSWalk, &normalized, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSRemove(ctx context.Context, params *FSRemoveParams) (*FSRemoveResponse, error) {
	if params == nil {
		return nil, errors.New("fs/remove params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/remove path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/remove: %w", err)
	}
	var response FSRemoveResponse
	err = c.call(ctx, MethodFSRemove, &normalized, &response)
	c.clearInFlightMetadataRequests()
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) FSCopy(ctx context.Context, params *FSCopyParams) (*FSCopyResponse, error) {
	if params == nil {
		return nil, errors.New("fs/copy params are required")
	}
	normalized := *params
	sourcePath, err := normalizeFSPathForWire(params.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("fs/copy sourcePath: %w", err)
	}
	destinationPath, err := normalizeFSPathForWire(params.DestinationPath)
	if err != nil {
		return nil, fmt.Errorf("fs/copy destinationPath: %w", err)
	}
	normalized.SourcePath = sourcePath
	normalized.DestinationPath = destinationPath
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/copy: %w", err)
	}
	var response FSCopyResponse
	err = c.call(ctx, MethodFSCopy, &normalized, &response)
	c.clearInFlightMetadataRequests()
	if err != nil {
		return nil, err
	}
	return &response, nil
}

func normalizeFSReadFileParams(params *FSReadFileParams) (*FSReadFileParams, error) {
	if params == nil {
		return nil, errors.New("fs/readFile params are required")
	}
	normalized := *params
	path, err := normalizeFSPathForWire(params.Path)
	if err != nil {
		return nil, fmt.Errorf("fs/readFile path: %w", err)
	}
	normalized.Path = path
	normalized.Sandbox, err = normalizeFSSandboxForWire(params.Sandbox)
	if err != nil {
		return nil, fmt.Errorf("fs/readFile: %w", err)
	}
	return &normalized, nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	c.conn = nil
	c.mu.Unlock()
	var err error
	if conn != nil {
		err = conn.Close()
	}
	if c.cleanup != nil {
		c.recoverMu.Lock()
		c.cleanup()
		c.recoverMu.Unlock()
	}
	c.failTerminal(errors.New("exec-server client is closed"))
	return err
}

func (c *Client) call(ctx context.Context, method string, params any, target any) error {
	if c == nil {
		return errors.New("exec-server client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.ensureConnected(ctx); err != nil {
		return err
	}
	id, responseCh, err := c.reserveCall()
	if err != nil {
		return err
	}
	request := clientRequestEnvelope(id, method, params, traceContextFromContext(ctx))
	data, err := json.Marshal(request)
	if err != nil {
		c.cancelCall(id)
		return err
	}
	if err := c.write(ctx, data); err != nil {
		c.cancelCall(id)
		return err
	}
	var result clientCallResult
	select {
	case result = <-responseCh:
	case <-ctx.Done():
		c.cancelCall(id)
		return ctx.Err()
	case <-c.done:
		select {
		case result = <-responseCh:
		default:
			return errors.New("exec-server client is closed")
		}
	}
	if result.err != nil {
		return result.err
	}
	response := result.response
	if response.Error != nil {
		return fmt.Errorf("exec-server %s failed (%d): %s", method, response.Error.Code, response.Error.Message)
	}
	if target == nil || len(response.Result) == 0 || string(response.Result) == "null" {
		return nil
	}
	return json.Unmarshal(response.Result, target)
}

// clientRequestEnvelope builds the JSON-RPC envelope for an outbound
// exec-server request, carrying the active trace context so the server can
// continue the client's trace (Rust #39098 trace-parent propagation).
func clientRequestEnvelope(id int64, method string, params any, trace TraceContext) map[string]any {
	envelope := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if !trace.IsZero() {
		envelope["traceContext"] = trace
	}
	return envelope
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	if c == nil {
		return errors.New("exec-server client is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.ensureConnected(ctx); err != nil {
		return err
	}
	request := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return c.write(ctx, data)
}

func (c *Client) ensureConnected(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("exec-server client is closed")
	}
	connected := c.conn != nil
	c.mu.Unlock()
	if connected {
		return nil
	}
	return c.recoverConnection(ctx)
}

func (c *Client) recoverConnection(ctx context.Context) error {
	c.recoverMu.Lock()
	defer c.recoverMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("exec-server client is closed")
	}
	if c.conn != nil {
		c.mu.Unlock()
		return nil
	}
	sessionID := c.sessionID
	opener := c.open
	c.mu.Unlock()
	if opener == nil {
		return errors.New("exec-server client has no reconnect strategy")
	}

	recoveryCtx := ctx
	var cancel context.CancelFunc
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > clientRecoveryTimeout {
		recoveryCtx, cancel = context.WithTimeout(ctx, clientRecoveryTimeout)
		defer cancel()
	}
	var lastErr error
	for {
		conn, initialized, err := opener(recoveryCtx, sessionID, c.handleNotification)
		if err == nil && initialized.SessionID != sessionID {
			_ = conn.CloseNow()
			err = fmt.Errorf("exec-server initialized an unexpected session %s", initialized.SessionID)
		}
		if err == nil {
			c.mu.Lock()
			if c.closed {
				c.mu.Unlock()
				_ = conn.CloseNow()
				return errors.New("exec-server client is closed")
			}
			if c.conn != nil {
				c.mu.Unlock()
				_ = conn.CloseNow()
				return nil
			}
			c.conn = conn
			c.mu.Unlock()
			go c.readLoop(conn)
			return nil
		}
		lastErr = err
		timer := time.NewTimer(clientRecoveryRetry)
		select {
		case <-recoveryCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return fmt.Errorf("exec-server session recovery failed: %w", lastErr)
			}
			return recoveryCtx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) reserveCall() (int64, chan clientCallResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return 0, nil, errors.New("exec-server client is closed")
	default:
	}
	id := c.nextID
	c.nextID++
	responseCh := make(chan clientCallResult, 1)
	c.pending[id] = responseCh
	return id, responseCh, nil
}

func (c *Client) cancelCall(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) write(ctx context.Context, data []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	conn := c.conn
	closed := c.closed
	c.mu.Unlock()
	if closed || conn == nil {
		return errors.New("exec-server client is disconnected")
	}
	if err := conn.Write(ctx, data); err != nil {
		return c.failTransport(conn, err)
	}
	return nil
}

func (c *Client) readLoop(conn clientConnection) {
	connectionDone := make(chan struct{})
	defer close(connectionDone)
	for {
		data, err := conn.Read(context.Background())
		if err != nil {
			_ = c.failTransport(conn, err)
			return
		}
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			_ = c.failTransport(conn, err)
			return
		}
		if envelope.Method != "" {
			if len(envelope.ID) == 0 {
				if err := c.handleNotification(envelope.Method, envelope.Params); err != nil {
					_ = c.failTransport(conn, err)
					return
				}
				continue
			}
			id, ok := clientResponseID(envelope.ID)
			if !ok || id < 0 {
				_ = c.failTransport(conn, errors.New("exec-server sent an invalid request ID"))
				return
			}
			if !c.admitInboundRequest(id) {
				_ = c.failTransport(conn, errors.New("exec-server reused an in-flight request ID"))
				return
			}
			select {
			case c.inboundSlots <- struct{}{}:
				go c.handleInboundRequest(conn, connectionDone, id, envelope.Method, envelope.Params)
			default:
				c.releaseInboundRequest(id, false)
				go c.respondInboundResult(conn, id, &NetworkPolicyRequestResponse{Decision: DenyNetworkPolicyDecision(NetworkPolicyDenialReason)})
			}
			continue
		}
		id, ok := clientResponseID(envelope.ID)
		if !ok {
			continue
		}
		var response clientResponse
		if err := json.Unmarshal(data, &response); err != nil {
			_ = c.failTransport(conn, err)
			return
		}
		c.mu.Lock()
		responseCh := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if responseCh != nil {
			responseCh <- clientCallResult{response: response}
		}
	}
}

func (c *Client) admitInboundRequest(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.inboundIDs[id]; exists {
		return false
	}
	c.inboundIDs[id] = struct{}{}
	return true
}

func (c *Client) releaseInboundRequest(id int64, releaseSlot bool) {
	c.mu.Lock()
	delete(c.inboundIDs, id)
	c.mu.Unlock()
	if releaseSlot {
		<-c.inboundSlots
	}
}

func (c *Client) handleInboundRequest(conn clientConnection, connectionDone <-chan struct{}, id int64, method string, raw json.RawMessage) {
	defer c.releaseInboundRequest(id, true)
	if method != MethodNetworkPolicyRequest {
		c.respondInboundError(conn, id, -32601, fmt.Sprintf("exec-server client does not implement `%s` yet", method))
		return
	}
	var params NetworkPolicyRequestParams
	if err := json.Unmarshal(raw, &params); err != nil || !validExecServerNetworkProtocol(params.Request.Protocol) {
		c.respondInboundError(conn, id, -32602, "invalid network policy request params")
		return
	}
	validProcess := params.ProcessID != "" && len(params.ProcessID) <= MaxNetworkPolicyProcessIDBytes
	validHost := params.Request.Host != "" && len(params.Request.Host) <= MaxNetworkPolicyHostBytes && !containsControlOrWhitespace(params.Request.Host) && params.Request.Port != 0
	c.mu.Lock()
	session := c.sessions[params.ProcessID]
	c.mu.Unlock()
	var controller *networkPolicyDecisionController
	var processDone <-chan struct{}
	if validProcess && validHost && session != nil {
		controller, processDone = session.networkPolicySnapshot()
	}
	decision := network.DenyProxyDecision(NetworkPolicyDenialReason)
	if controller != nil && controller.decider != nil && processDone != nil {
		decisionCh := make(chan network.ProxyDecision, 1)
		requestCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { decisionCh <- controller.decider.Decide(requestCtx, params.Request.ProxyRequest("")) }()
		timer := time.NewTimer(controller.timeout)
		defer timer.Stop()
		select {
		case <-connectionDone:
			return
		case <-processDone:
		case <-timer.C:
		case decision = <-decisionCh:
		}
	}
	if session != nil {
		c.mu.Lock()
		current := c.sessions[params.ProcessID]
		c.mu.Unlock()
		if current != session {
			return
		}
	}
	c.respondInboundResult(conn, id, &NetworkPolicyRequestResponse{Decision: execServerDecisionFromProxy(decision)})
}

func validExecServerNetworkProtocol(protocol ExecServerNetworkProtocol) bool {
	switch protocol {
	case NetworkProtocolHTTP, NetworkProtocolHTTPSConnect, NetworkProtocolSOCKS5TCP, NetworkProtocolSOCKS5UDP:
		return true
	default:
		return false
	}
}

func execServerDecisionFromProxy(decision network.ProxyDecision) ExecServerNetworkPolicyDecision {
	if decision.Allow {
		return AllowNetworkPolicyDecision()
	}
	reason := decision.Reason
	if reason == "" || len(reason) > MaxNetworkPolicyReasonBytes || containsControl(reason) {
		reason = NetworkPolicyDenialReason
	}
	if decision.Decision == network.ProxyPolicyDecisionAsk {
		return AskNetworkPolicyDecision(reason)
	}
	return DenyNetworkPolicyDecision(reason)
}

func (c *Client) respondInboundResult(conn clientConnection, id int64, result any) {
	c.respondInbound(conn, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *Client) respondInboundError(conn clientConnection, id int64, code int, message string) {
	c.respondInbound(conn, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (c *Client) respondInbound(conn clientConnection, response any) {
	data, err := json.Marshal(response)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	current := c.conn
	closed := c.closed
	c.mu.Unlock()
	if closed || current != conn {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := conn.Write(ctx, data); err != nil {
		_ = c.failTransport(conn, err)
	}
}

func (c *Client) handleNotification(method string, raw json.RawMessage) error {
	var event ProcessEvent
	switch method {
	case MethodProcessOutput:
		if err := requireNotificationFields(raw, "processId", "seq", "stream", "chunk"); err != nil {
			return err
		}
		var params ProcessOutputNotification
		if err := json.Unmarshal(raw, &params); err != nil {
			return err
		}
		if params.Stream != "stdout" && params.Stream != "stderr" && params.Stream != "pty" {
			return fmt.Errorf("invalid process output stream %q", params.Stream)
		}
		if _, err := base64.StdEncoding.DecodeString(params.Chunk); err != nil {
			return fmt.Errorf("invalid process output chunk: %w", err)
		}
		event = ProcessEvent{Kind: ProcessEventOutput, ProcessID: params.ProcessID, Seq: params.Seq, Stream: params.Stream, Chunk: params.Chunk}
	case MethodProcessExited:
		if err := requireNotificationFields(raw, "processId", "seq", "exitCode"); err != nil {
			return err
		}
		var params ProcessExitedNotification
		if err := json.Unmarshal(raw, &params); err != nil {
			return err
		}
		event = ProcessEvent{Kind: ProcessEventExited, ProcessID: params.ProcessID, Seq: params.Seq, ExitCode: params.ExitCode, SandboxDenied: params.SandboxDenied}
	case MethodProcessClosed:
		if err := requireNotificationFields(raw, "processId", "seq"); err != nil {
			return err
		}
		var params ProcessClosedNotification
		if err := json.Unmarshal(raw, &params); err != nil {
			return err
		}
		event = ProcessEvent{Kind: ProcessEventClosed, ProcessID: params.ProcessID, Seq: params.Seq}
	case MethodHTTPRequestBodyDelta:
		if err := requireNotificationFields(raw, "requestId", "seq", "deltaBase64"); err != nil {
			return err
		}
		var params HTTPRequestBodyDeltaNotification
		if err := json.Unmarshal(raw, &params); err != nil {
			return err
		}
		if _, err := base64.StdEncoding.DecodeString(params.DeltaBase64); err != nil {
			return fmt.Errorf("invalid http response body delta: %w", err)
		}
		c.mu.Lock()
		stream := c.httpStreams[params.RequestID]
		c.mu.Unlock()
		if stream != nil {
			stream.publish(params)
			if params.Done || params.Error != nil {
				stream.removeRoute()
			}
		}
		return nil
	case MethodNetworkPolicyDecision:
		var params NetworkPolicyDecisionNotification
		if err := json.Unmarshal(raw, &params); err != nil {
			return err
		}
		if !params.Validate() {
			return fmt.Errorf("invalid network policy decision notification")
		}
		c.mu.Lock()
		active := c.sessions[params.ProcessID] != nil
		c.mu.Unlock()
		if !active {
			return nil
		}
		emitNetworkPolicyDecisionAudit(params)
		return nil
	default:
		return nil
	}
	c.mu.Lock()
	session := c.sessions[event.ProcessID]
	c.mu.Unlock()
	if session != nil {
		session.publishOrdered(event)
	}
	return nil
}

func emitNetworkPolicyDecisionAudit(decision NetworkPolicyDecisionNotification) {
	method := "none"
	if decision.Method != nil {
		method = *decision.Method
	}
	client := "unknown"
	if decision.Client != nil {
		client = *decision.Client
	}
	// Rust 899d1715c8 (#38800): forwarded network policy decisions are audit
	// telemetry. The codex_otel.log_only target keeps them available to OTEL
	// log export while the state log DB handler excludes them from persistent
	// logs (state/persistSlogRecord).
	slog.Info("codex.network_proxy.policy_decision",
		"target", "codex_otel.log_only",
		"event.name", "codex.network_proxy.policy_decision",
		"event.timestamp", decision.Timestamp,
		"execution.id", decision.ProcessID,
		"network.policy.scope", decision.Scope,
		"network.policy.decision", decision.Decision,
		"network.policy.source", decision.Source,
		"network.policy.reason", decision.Reason,
		"network.policy.override", decision.PolicyOverride,
		"network.transport.protocol", string(decision.Protocol),
		"server.address", decision.Host,
		"server.port", decision.Port,
		"http.request.method", method,
		"client.address", client,
	)
}

func requireNotificationFields(raw json.RawMessage, fields ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	for _, field := range fields {
		value, ok := object[field]
		if !ok || len(value) == 0 || string(value) == "null" {
			return fmt.Errorf("process notification is missing required field %s", field)
		}
	}
	return nil
}

func (s *clientProcessSession) publishOrdered(event ProcessEvent) {
	s.mu.Lock()
	if s.closed || event.Seq <= s.lastPublished {
		s.mu.Unlock()
		return
	}
	s.pending[event.Seq] = event
	ready := make([]ProcessEvent, 0, 1)
	for {
		next := s.lastPublished + 1
		event, ok := s.pending[next]
		if !ok {
			break
		}
		delete(s.pending, next)
		s.lastPublished = next
		ready = append(ready, event)
		if event.Kind == ProcessEventClosed {
			s.closed = true
			break
		}
	}
	subscription := s.subscription
	s.mu.Unlock()
	for _, event := range ready {
		subscription.publish(event)
	}
}

func (c *Client) failTransport(conn clientConnection, err error) error {
	if err == nil {
		err = errors.New("exec-server transport disconnected")
	}
	transportErr := &clientTransportError{err: err}
	c.mu.Lock()
	if c.conn != conn {
		c.mu.Unlock()
		return transportErr
	}
	c.conn = nil
	pending := c.pending
	c.pending = map[int64]chan clientCallResult{}
	httpStreams := c.httpStreams
	c.httpStreams = map[string]*HTTPBodyStream{}
	c.mu.Unlock()
	_ = conn.CloseNow()
	failClientInFlight(pending, nil, httpStreams, transportErr)
	go func() {
		if recoveryErr := c.recoverConnection(context.Background()); recoveryErr != nil {
			c.failProcessSessions(recoveryErr)
			return
		}
		c.recoverProcessSessions()
	}()
	return transportErr
}

func (c *Client) failTerminal(err error) {
	if err == nil {
		err = errors.New("exec-server client is closed")
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		pending, sessions, httpStreams := c.takeInFlightLocked()
		close(c.done)
		c.mu.Unlock()
		failClientInFlight(pending, sessions, httpStreams, err)
	})
}

func (c *Client) takeInFlightLocked() (map[int64]chan clientCallResult, map[string]*clientProcessSession, map[string]*HTTPBodyStream) {
	pending := c.pending
	c.pending = map[int64]chan clientCallResult{}
	sessions := c.sessions
	c.sessions = map[string]*clientProcessSession{}
	httpStreams := c.httpStreams
	c.httpStreams = map[string]*HTTPBodyStream{}
	return pending, sessions, httpStreams
}

func failClientInFlight(
	pending map[int64]chan clientCallResult,
	sessions map[string]*clientProcessSession,
	httpStreams map[string]*HTTPBodyStream,
	err error,
) {
	for _, responseCh := range pending {
		responseCh <- clientCallResult{err: err}
	}
	for _, session := range sessions {
		if session != nil && session.subscription != nil {
			session.cancelNetworkPolicy()
			session.subscription.close(err)
		}
	}
	for _, stream := range httpStreams {
		if stream != nil {
			stream.fail(err)
		}
	}
}

func (c *Client) recoverProcessSessions() {
	c.mu.Lock()
	sessions := make(map[string]*clientProcessSession, len(c.sessions))
	for processID, session := range c.sessions {
		sessions[processID] = session
	}
	c.mu.Unlock()
	for processID, session := range sessions {
		session.mu.Lock()
		after := session.lastPublished
		closed := session.closed
		session.mu.Unlock()
		if closed {
			continue
		}
		response, err := c.Read(context.Background(), &ReadParams{ProcessID: processID, AfterSeq: &after})
		if err != nil {
			c.failProcessSession(processID, session, err)
			continue
		}
		if !publishRecoveredProcessResponse(processID, session, after, response) {
			c.failProcessSession(processID, session, fmt.Errorf("exec-server process %s recovery output has a sequence gap after %d", processID, after))
		}
	}
}

func publishRecoveredProcessResponse(processID string, session *clientProcessSession, after uint64, response *ReadResponse) bool {
	if response == nil {
		return false
	}
	expected := after + 1
	for _, chunk := range response.Chunks {
		if chunk.Seq < expected {
			continue
		}
		if chunk.Seq != expected {
			return false
		}
		session.publishOrdered(ProcessEvent{
			Kind:      ProcessEventOutput,
			ProcessID: processID,
			Seq:       chunk.Seq,
			Stream:    chunk.Stream,
			Chunk:     chunk.Chunk,
		})
		expected++
	}
	if response.Failure != nil {
		return false
	}
	if response.Exited {
		exitSeq := response.NextSeq - 1
		if response.Closed {
			exitSeq--
		}
		if exitSeq >= expected {
			if exitSeq != expected || response.ExitCode == nil {
				return false
			}
			denied := response.SandboxDenied
			session.publishOrdered(ProcessEvent{Kind: ProcessEventExited, ProcessID: processID, Seq: exitSeq, ExitCode: *response.ExitCode, SandboxDenied: &denied})
			expected++
		}
	}
	if response.Closed {
		closedSeq := response.NextSeq - 1
		if closedSeq >= expected {
			if closedSeq != expected {
				return false
			}
			session.publishOrdered(ProcessEvent{Kind: ProcessEventClosed, ProcessID: processID, Seq: closedSeq})
			expected++
		}
	}
	return expected == response.NextSeq
}

func (c *Client) failProcessSessions(err error) {
	c.mu.Lock()
	sessions := c.sessions
	c.sessions = map[string]*clientProcessSession{}
	c.mu.Unlock()
	for _, session := range sessions {
		if session != nil && session.subscription != nil {
			session.cancelNetworkPolicy()
			session.subscription.close(err)
		}
	}
}

func (c *Client) failProcessSession(processID string, session *clientProcessSession, err error) {
	c.mu.Lock()
	if c.sessions[processID] == session {
		delete(c.sessions, processID)
	}
	c.mu.Unlock()
	if session != nil && session.subscription != nil {
		session.cancelNetworkPolicy()
		session.subscription.close(err)
	}
}

func clientResponseID(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 {
		return 0, false
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, false
	}
	return id, true
}
