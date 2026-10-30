package realtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const realtimeMultipartBoundary = "codex-realtime-call-boundary"

// realtimeDialError carries the HTTP status of a rejected realtime websocket
// handshake so callers can distinguish retryable activation failures.
type realtimeDialError struct {
	statusCode int
	err        error
}

func (e *realtimeDialError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *realtimeDialError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

type TransportBackendConfig struct {
	WebsocketBaseURL  string
	WebRTCCallBaseURL string
	SidebandBaseURL   string
	HTTPClient        *http.Client
	Headers           http.Header
	CallHeaders       http.Header
	QueryParams       map[string]string
	PrepareCall       func(context.Context, *http.Request, []byte) error
}

type realtimeWebRTCCall struct {
	SDP    string
	CallID string
}

type realtimeSideband struct {
	threadID string
	callID   string
	// existingCall marks a client-created call whose own session configuration
	// must not be overwritten by the sideband.
	existingCall bool
	backend      *TransportBackendConfig
	config       *SessionConfig
	ctx          context.Context
	cancel       context.CancelFunc
}

type realtimeTransportSession struct {
	threadID              string
	config                SessionConfig
	conn                  *websocket.Conn
	ctx                   context.Context
	cancel                context.CancelFunc
	pendingEvents         []Event
	writeMu               sync.Mutex
	closeOnce             sync.Once
	stateMu               sync.Mutex
	transcript            activeRealtimeTranscript
	newInputEntry         bool
	newOutputEntry        bool
	tailFlushed           bool
	activeHandoffID       string
	lastCodexOutput       string
	responseActive        bool
	responseCreatePending bool
	outputAudioItemID     string
	outputAudioEndMS      uint32
}

type realtimeAudioTruncate struct {
	itemID     string
	audioEndMS uint32
}

func (s *realtimeTransportSession) closeNow() {
	if s == nil {
		return
	}
	s.cancel()
	if s.conn != nil {
		s.conn.CloseNow()
	}
}

// realtimeTransportCloseTimeout bounds how long a detached observer may spend
// completing the websocket close handshake (Rust #57817).
const realtimeTransportCloseTimeout = 5 * time.Second

// closeTransport sends a normal close frame to the peer and releases the
// socket. A detached sideband observer must not end the frontend-owned realtime
// session, so cancellation paths use this instead of an abrupt close
// (Rust #57817 `close_transport`).
func (s *realtimeTransportSession) closeTransport() {
	if s == nil {
		return
	}
	if s.conn != nil {
		// coder/websocket bounds the close handshake internally; the timeout
		// above documents the budget Rust applies around the same call.
		_ = s.conn.Close(websocket.StatusNormalClosure, "")
		s.conn = nil
	}
	s.cancel()
}

func (m *Manager) SetTransportBackend(config *TransportBackendConfig) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.transport = cloneTransportBackendConfig(config)
	m.mu.Unlock()
}

func (m *Manager) SetNotificationSink(sink func(Notification)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.notificationSink = sink
	m.mu.Unlock()
}

func (m *Manager) SetEventSink(sink func(string, Event)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.eventSink = sink
	m.mu.Unlock()
}

func (m *Manager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.shutdown = true
	connections := make([]*realtimeTransportSession, 0, len(m.connections))
	sidebands := make([]*realtimeSideband, 0, len(m.sidebands))
	for key, stream := range m.streams {
		if stream != nil && stream.Timer != nil {
			stream.Timer.Stop()
		}
		delete(m.streams, key)
	}
	for threadID, connection := range m.connections {
		if connection != nil {
			connections = append(connections, connection)
		}
		delete(m.connections, threadID)
	}
	for threadID, sideband := range m.sidebands {
		if sideband != nil {
			sidebands = append(sidebands, sideband)
		}
		delete(m.sidebands, threadID)
	}
	m.mu.Unlock()
	for _, connection := range connections {
		m.flushRealtimeTranscriptTail(connection)
		connection.closeNow()
	}
	for _, sideband := range sidebands {
		sideband.cancel()
	}
	done := make(chan struct{})
	go func() {
		m.backgroundTaskWait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cloneTransportBackendConfig(config *TransportBackendConfig) *TransportBackendConfig {
	if config == nil {
		return nil
	}
	clone := *config
	clone.WebsocketBaseURL = strings.TrimSpace(clone.WebsocketBaseURL)
	clone.WebRTCCallBaseURL = strings.TrimSpace(clone.WebRTCCallBaseURL)
	clone.SidebandBaseURL = strings.TrimSpace(clone.SidebandBaseURL)
	if clone.WebsocketBaseURL == "" && clone.WebRTCCallBaseURL == "" && clone.SidebandBaseURL == "" {
		return nil
	}
	clone.Headers = config.Headers.Clone()
	if clone.Headers == nil {
		clone.Headers = make(http.Header)
	}
	clone.CallHeaders = config.CallHeaders.Clone()
	if clone.CallHeaders == nil {
		clone.CallHeaders = clone.Headers.Clone()
	}
	if clone.HTTPClient == nil {
		clone.HTTPClient = &http.Client{}
	}
	clone.QueryParams = make(map[string]string, len(config.QueryParams))
	for key, value := range config.QueryParams {
		clone.QueryParams[key] = value
	}
	return &clone
}

// realtimeSidebandBackendForTransport applies an existing call's in-process
// sideband endpoint override (Rust #41923). The override wins over the
// configured endpoint for this call, while other transports keep using the
// configured or default endpoint. Runtime authentication headers are preserved
// because the clone keeps the backend's header configuration.
func realtimeSidebandBackendForTransport(backend *TransportBackendConfig, transport StartTransport) *TransportBackendConfig {
	override := strings.TrimSpace(transport.SidebandBaseURL)
	if transport.Type != "existingCall" || override == "" {
		return backend
	}
	clone := cloneTransportBackendConfig(backend)
	if clone == nil {
		clone = &TransportBackendConfig{
			Headers:     make(http.Header),
			HTTPClient:  &http.Client{},
			QueryParams: map[string]string{},
		}
	}
	clone.SidebandBaseURL = override
	return clone
}

func createRealtimeWebRTCCall(ctx context.Context, backend *TransportBackendConfig, config *SessionConfig) (*realtimeWebRTCCall, error) {
	endpoint, err := realtimeCallURL(backend.WebRTCCallBaseURL, config)
	if err != nil {
		return nil, err
	}
	body, contentType, err := realtimeCallBody(backend.WebRTCCallBaseURL, config)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create realtime call request: %w", err)
	}
	request.Header = backend.CallHeaders.Clone()
	request.Header.Set("Content-Type", contentType)
	// Rust passes the realtime session headers to the WebRTC call request too
	// (RealtimeStart::extra_headers), including the client voice session id
	// (#53784).
	applyRealtimeSessionHeaders(request.Header, config)
	if backend.PrepareCall != nil {
		if err := backend.PrepareCall(ctx, request, body); err != nil {
			return nil, fmt.Errorf("authenticate realtime call: %w", err)
		}
	}
	response, err := backend.HTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("create realtime call: %w", err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read realtime call response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("create realtime call: HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(answer)))
	}
	callID, err := realtimeCallID(response.Header.Get("Location"))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(answer)) == "" {
		return nil, fmt.Errorf("create realtime call: empty SDP answer")
	}
	return &realtimeWebRTCCall{SDP: string(answer), CallID: callID}, nil
}

func realtimeCallBody(baseURL string, config *SessionConfig) ([]byte, string, error) {
	update := realtimeSessionUpdate(config)
	sessionValue, _ := update["session"].(map[string]any)
	delete(sessionValue, "id")
	if strings.Contains(baseURL, "/backend-api") {
		body, err := json.Marshal(map[string]any{"sdp": config.Transport.SDP, "session": sessionValue})
		if err != nil {
			return nil, "", fmt.Errorf("encode realtime call: %w", err)
		}
		return body, "application/json", nil
	}
	session, err := json.Marshal(sessionValue)
	if err != nil {
		return nil, "", fmt.Errorf("encode realtime session: %w", err)
	}
	var body bytes.Buffer
	fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"sdp\"\r\nContent-Type: application/sdp\r\n\r\n%s\r\n", realtimeMultipartBoundary, config.Transport.SDP)
	fmt.Fprintf(&body, "--%s\r\nContent-Disposition: form-data; name=\"session\"\r\nContent-Type: application/json\r\n\r\n%s\r\n", realtimeMultipartBoundary, session)
	fmt.Fprintf(&body, "--%s--\r\n", realtimeMultipartBoundary)
	return body.Bytes(), "multipart/form-data; boundary=" + realtimeMultipartBoundary, nil
}

func realtimeCallURL(base string, config *SessionConfig) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid realtime call base URL %q", base)
	}
	cleaned := strings.TrimSuffix(parsed.Path, "/")
	pathSuffix := "/realtime/calls"
	usesBackendRequestShape := strings.Contains(base, "/backend-api")
	if config != nil && config.Version == VersionV3 && !usesBackendRequestShape {
		pathSuffix = "/live"
	}
	switch {
	case strings.HasSuffix(cleaned, pathSuffix):
	case cleaned == "" || cleaned == "/":
		parsed.Path = "/v1" + pathSuffix
	default:
		parsed.Path = cleaned + pathSuffix
	}
	if config != nil && (config.Version == VersionV1 || (config.Version == VersionV3 && usesBackendRequestShape)) {
		query := parsed.Query()
		query.Set("intent", "quicksilver")
		query.Set("architecture", "avas")
		parsed.RawQuery = query.Encode()
	}
	return parsed.String(), nil
}

func realtimeCallID(location string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(location))
	if err != nil {
		return "", fmt.Errorf("invalid realtime call location %q: %w", location, err)
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		if isRealtimeCallID(segments[i]) {
			return segments[i], nil
		}
	}
	return "", fmt.Errorf("realtime call Location does not contain a call id: %s", location)
}

func isRealtimeCallID(value string) bool {
	if strings.HasPrefix(value, "rtc_") && len(value) > len("rtc_") {
		return true
	}
	if len(value) != 36 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if value[index] != '-' {
				return false
			}
			continue
		}
		character := value[index]
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func newRealtimeSideband(threadID, callID string, backend *TransportBackendConfig, config *SessionConfig) *realtimeSideband {
	ctx, cancel := context.WithCancel(context.Background())
	cloned := *config
	sidebandBackend := cloneTransportBackendConfig(backend)
	if sidebandBackend != nil && len(sidebandBackend.CallHeaders) > 0 {
		sidebandBackend.Headers = sidebandBackend.CallHeaders.Clone()
	}
	return &realtimeSideband{threadID: threadID, callID: callID, backend: sidebandBackend, config: &cloned, ctx: ctx, cancel: cancel}
}

func (m *Manager) startRealtimeSideband(sideband *realtimeSideband) {
	if m == nil || sideband == nil {
		return
	}
	go func() {
		defer m.backgroundTaskWait.Done()
		// Rust #39257: only frameless sidebands reconnect after unexpected
		// transport loss, with uncapped attempts and capped exponential
		// backoff. A terminal handshake status (404/410) completes the session.
		rapidDisconnects := 0
		for {
			initialization := realtimeInitializeLegacyWebrtcSideband
			if sideband.existingCall {
				initialization = realtimeInitializeExistingCall
			}
			var (
				connection *realtimeTransportSession
				err        error
			)
			if initialization == realtimeInitializeExistingCall {
				connection, err = dialExistingCallSideband(sideband.ctx, sideband)
			} else {
				connection, err = dialRealtimeTransport(sideband.ctx, sideband.threadID, sideband.backend, sideband.config, sideband.callID, initialization)
			}
			if err != nil {
				if realtimeHandshakeTerminal(err) {
					return
				}
				if rapidDisconnects == 0 {
					m.notifySidebandError(sideband, fmt.Errorf("connect realtime sideband: %w", err))
				}
				if sideband.ctx.Err() != nil || !m.sidebandShouldReconnect(sideband) {
					return
				}
				rapidDisconnects++
				if !sleepSidebandReconnect(sideband.ctx, realtimeSidebandReconnectDelay(rapidDisconnects)) {
					return
				}
				continue
			}
			connectedAt := time.Now()
			m.mu.Lock()
			state := m.sessions[sideband.threadID]
			if m.shutdown || sideband.ctx.Err() != nil || m.sidebands[sideband.threadID] != sideband || state == nil || state.ClosedAt != nil {
				m.mu.Unlock()
				// Cancelled before the observer attached: release the socket with a
				// normal close frame instead of ending the session (Rust #57817).
				connection.closeTransport()
				return
			}
			m.connections[sideband.threadID] = connection
			m.mu.Unlock()
			// runRealtimeConnection returns only after the connection ends.
			// finishRealtimeTransport marks the session closed; reset that so
			// the sideband can reconnect and continue.
			m.runRealtimeConnection(connection)
			m.mu.Lock()
			sessionState := m.sessions[sideband.threadID]
			if sessionState != nil {
				sessionState.ClosedAt = nil
				sessionState.CloseReason = ""
			}
			m.sidebands[sideband.threadID] = sideband
			m.mu.Unlock()
			if sideband.ctx.Err() != nil || !m.sidebandShouldReconnect(sideband) {
				return
			}
			// Only frameless (live) transports keep reconnecting; a legacy
			// sideband reports the loss and finishes the session.
			if sideband.config != nil && sideband.config.Version != VersionV3 {
				return
			}
			if time.Since(connectedAt) >= realtimeSidebandStableConnection {
				rapidDisconnects = 0
			}
			rapidDisconnects++
			if !sleepSidebandReconnect(sideband.ctx, realtimeSidebandReconnectDelay(rapidDisconnects)) {
				return
			}
		}
	}()
}

// realtimeSidebandStableConnection is how long a sideband must stay connected
// before its disconnect history is forgotten.
const realtimeSidebandStableConnection = 30 * time.Second

// realtimeSidebandReconnectBase matches the Rust reconnect base delay.
const realtimeSidebandReconnectBase = 200 * time.Millisecond

func (m *Manager) sidebandShouldReconnect(sideband *realtimeSideband) bool {
	if m == nil || sideband == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.sessions[sideband.threadID]
	return m.sidebands[sideband.threadID] == sideband && !m.shutdown && sideband.ctx.Err() == nil && state != nil && state.ClosedAt == nil
}

func realtimeSidebandReconnectDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 5)
	delay := realtimeSidebandReconnectBase * time.Duration(1<<shift)
	if delay <= 0 || delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

func sleepSidebandReconnect(ctx context.Context, delay time.Duration) bool {
	if ctx == nil {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// realtimeExistingCallActivationDelays is the bounded retry budget for joining a
// client-created call whose sideband can briefly return 404 while ICE activates
// the call (Rust #51460: 100/300/800 ms).
var realtimeExistingCallActivationDelays = []time.Duration{
	100 * time.Millisecond,
	300 * time.Millisecond,
	800 * time.Millisecond,
}

// dialExistingCallSideband joins a client-created call, retrying a 404 handshake
// up to three times while the call activates. Any other failure and an exhausted
// activation budget return the original error.
func dialExistingCallSideband(ctx context.Context, sideband *realtimeSideband) (*realtimeTransportSession, error) {
	if sideband == nil {
		return nil, fmt.Errorf("connect realtime sideband: missing sideband")
	}
	var lastErr error
	for attempt := 0; attempt <= len(realtimeExistingCallActivationDelays); attempt++ {
		connection, err := dialRealtimeTransport(ctx, sideband.threadID, sideband.backend, sideband.config, sideband.callID, realtimeInitializeExistingCall)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		if !realtimeHandshakeNotFound(err) || attempt == len(realtimeExistingCallActivationDelays) {
			return nil, lastErr
		}
		if !sleepSidebandReconnect(ctx, realtimeExistingCallActivationDelays[attempt]) {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

func realtimeHandshakeTerminal(err error) bool {
	if err == nil {
		return false
	}
	if status, ok := realtimeHandshakeStatus(err); ok {
		return status == http.StatusNotFound || status == http.StatusGone
	}
	text := err.Error()
	return strings.Contains(text, "404") || strings.Contains(text, "410") ||
		strings.Contains(text, "not found") || strings.Contains(text, "gone")
}

// realtimeHandshakeNotFound reports whether a handshake failed because the call
// is not yet activated (HTTP 404).
func realtimeHandshakeNotFound(err error) bool {
	if err == nil {
		return false
	}
	if status, ok := realtimeHandshakeStatus(err); ok {
		return status == http.StatusNotFound
	}
	text := err.Error()
	return strings.Contains(text, "404") || strings.Contains(text, "not found")
}

func realtimeHandshakeStatus(err error) (int, bool) {
	var dialErr *realtimeDialError
	if errors.As(err, &dialErr) && dialErr.statusCode != 0 {
		return dialErr.statusCode, true
	}
	return 0, false
}

func (m *Manager) startRealtimeConnection(connection *realtimeTransportSession) {
	if m == nil || connection == nil {
		return
	}
	go func() {
		defer m.backgroundTaskWait.Done()
		m.runRealtimeConnection(connection)
	}()
}

func (m *Manager) runRealtimeConnection(connection *realtimeTransportSession) {
	defer func() {
		// Rust #57817: a cancelled observer closes only its transport so a
		// frontend-owned session survives the detach.
		if connection.ctx.Err() != nil {
			connection.closeTransport()
			return
		}
		connection.closeNow()
	}()
	for _, event := range connection.pendingEvents {
		m.handleRealtimeTransportEvent(connection, event)
	}
	connection.pendingEvents = nil
	for {
		messageType, payload, err := connection.conn.Read(connection.ctx)
		if err != nil {
			if connection.ctx.Err() == nil {
				m.flushRealtimeTranscriptTail(connection)
				status := websocket.CloseStatus(err)
				if status != websocket.StatusNormalClosure && status != websocket.StatusGoingAway {
					m.finishRealtimeTransport(connection, "error", fmt.Errorf("read realtime transport: %w", err))
					return
				}
				m.finishRealtimeTransport(connection, "transport_closed", nil)
			}
			return
		}
		event := Event{}
		ok := false
		if messageType == websocket.MessageBinary {
			event = Event{Type: "error", Message: "unexpected binary realtime websocket event"}
			ok = true
		} else {
			event, ok = parseRealtimeWireEvent(connection.config.Version, payload)
		}
		if !ok {
			continue
		}
		var steerHandoffID string
		var sendDeferredResponse bool
		var audioTruncate *realtimeAudioTruncate
		event, steerHandoffID, sendDeferredResponse, audioTruncate = connection.updateTranscript(event)
		if audioTruncate != nil {
			_ = connection.writeJSON(map[string]any{
				"type":          "conversation.item.truncate",
				"item_id":       audioTruncate.itemID,
				"content_index": 0,
				"audio_end_ms":  audioTruncate.audioEndMS,
			})
		}
		if steerHandoffID != "" {
			if err := connection.sendV2FunctionOutput(steerHandoffID, "This was sent to steer the previous background agent task."); err != nil {
				m.finishRealtimeTransport(connection, "error", err)
				return
			}
			if err := connection.requestResponseCreate(); err != nil {
				m.finishRealtimeTransport(connection, "error", err)
				return
			}
		}
		if event.Type == "noop.requested" && connection.config.Version == VersionV2 {
			if err := connection.sendV2FunctionOutput(event.HandoffID, ""); err != nil {
				m.finishRealtimeTransport(connection, "error", err)
				return
			}
		}
		if sendDeferredResponse {
			if err := connection.writeJSON(map[string]any{"type": "response.create"}); err != nil {
				m.finishRealtimeTransport(connection, "error", err)
				return
			}
		}
		m.handleRealtimeTransportEvent(connection, event)
		if event.Type == "error" {
			m.flushRealtimeTranscriptTail(connection)
			m.finishRealtimeTransport(connection, "error", nil)
			return
		}
	}
}

func (s *realtimeTransportSession) updateTranscript(event Event) (Event, string, bool, *realtimeAudioTruncate) {
	if s == nil {
		return event, "", false, nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	steerHandoffID := ""
	sendDeferredResponse := false
	var audioTruncate *realtimeAudioTruncate
	switch event.Type {
	case "input_audio_buffer.speech_started":
		s.newInputEntry = true
		if s.config.Version == VersionV2 && s.outputAudioItemID != "" && (event.ItemID == "" || event.ItemID == s.outputAudioItemID) {
			audioTruncate = &realtimeAudioTruncate{itemID: s.outputAudioItemID, audioEndMS: s.outputAudioEndMS}
			s.outputAudioItemID = ""
			s.outputAudioEndMS = 0
		}
	case "audio.out":
		if s.config.Version == VersionV2 && event.Audio != nil {
			s.updateOutputAudioState(event.Audio)
		}
	case "input_transcript.delta":
		s.transcript.appendDelta("user", event.Delta, s.newInputEntry, s.config.Version == VersionV3)
		if event.Delta != "" || s.config.Version != VersionV3 {
			s.newInputEntry = false
		}
	case "output_transcript.delta":
		s.transcript.appendDelta("assistant", event.Delta, s.newOutputEntry, s.config.Version == VersionV3)
		if event.Delta != "" || s.config.Version != VersionV3 {
			s.newOutputEntry = false
		}
	case "input_transcript.done":
		s.transcript.applyDone("user", event.Text, s.newInputEntry, s.config.Version == VersionV3)
		s.newInputEntry = s.config.Version == VersionV3
	case "output_transcript.done":
		s.transcript.applyDone("assistant", event.Text, s.newOutputEntry, s.config.Version == VersionV3)
		s.newOutputEntry = s.config.Version == VersionV3
	case "response.created":
		s.newOutputEntry = true
		s.responseActive = true
	case "response.done", "response.cancelled":
		s.outputAudioItemID = ""
		s.outputAudioEndMS = 0
		s.responseActive = false
		if s.responseCreatePending {
			s.responseCreatePending = false
			s.responseActive = true
			sendDeferredResponse = true
		}
	case "handoff.requested":
		s.outputAudioItemID = ""
		s.outputAudioEndMS = 0
		event.ActiveTranscript = s.transcript.takeHandoff(event.InputTranscript)
		s.newInputEntry = true
		s.newOutputEntry = true
		if s.config.Version == VersionV2 && s.activeHandoffID != "" {
			steerHandoffID = event.HandoffID
		} else {
			s.activeHandoffID = event.HandoffID
			s.lastCodexOutput = ""
		}
	}
	if event.Type == "noop.requested" {
		s.outputAudioItemID = ""
		s.outputAudioEndMS = 0
	}
	if s.config.Version == VersionV3 &&
		(event.Type == "input_transcript.done" || event.Type == "output_transcript.done") {
		// Rust #58221: a completed transcript travels with its event so the
		// fanout persists it before a later handoff can consume it.
		event.CompletedTranscript = s.transcript.takeCompletedTranscript(
			event.Type,
			s.newInputEntry,
			s.newOutputEntry,
			realtimeDelegationFieldBytes,
		)
	}
	return event, steerHandoffID, sendDeferredResponse, audioTruncate
}

func (s *realtimeTransportSession) updateOutputAudioState(frame *AudioChunk) {
	if s == nil || frame == nil || frame.ItemID == nil || strings.TrimSpace(*frame.ItemID) == "" {
		return
	}
	samples := uint64(0)
	if frame.SamplesPerChannel != nil {
		samples = uint64(*frame.SamplesPerChannel)
	} else if decoded, err := base64.StdEncoding.DecodeString(frame.Data); err == nil {
		channels := uint64(frame.NumChannels)
		if channels == 0 {
			channels = 1
		}
		samples = uint64(len(decoded)) / 2 / channels
	}
	if samples == 0 {
		return
	}
	sampleRate := uint64(frame.SampleRate)
	if sampleRate == 0 {
		sampleRate = 1
	}
	duration := samples * 1000 / sampleRate
	if duration == 0 {
		return
	}
	itemID := strings.TrimSpace(*frame.ItemID)
	if s.outputAudioItemID == itemID {
		total := uint64(s.outputAudioEndMS) + duration
		if total > uint64(^uint32(0)) {
			total = uint64(^uint32(0))
		}
		s.outputAudioEndMS = uint32(total)
		return
	}
	s.outputAudioItemID = itemID
	if duration > uint64(^uint32(0)) {
		duration = uint64(^uint32(0))
	}
	s.outputAudioEndMS = uint32(duration)
}

// realtimeDelegationFieldBytes mirrors Rust's RealtimeDelegation::FIELD_BYTES
// (#58221): completed transcript chunks are flushed once the active transcript
// grows past one delegation field.
const realtimeDelegationFieldBytes = 4 * 1024

type activeRealtimeTranscript struct {
	entries []TranscriptEntry
	// flushedUserInputs holds hashes of user utterances already written to
	// child history, so an echoed handoff input is not recorded twice.
	flushedUserInputs map[uint64]struct{}
}

func (t *activeRealtimeTranscript) findEntry(role string, frameless bool) *TranscriptEntry {
	if t == nil {
		return nil
	}
	if frameless {
		for index := len(t.entries) - 1; index >= 0; index-- {
			if t.entries[index].Role == role {
				return &t.entries[index]
			}
		}
		return nil
	}
	if len(t.entries) == 0 || t.entries[len(t.entries)-1].Role != role {
		return nil
	}
	return &t.entries[len(t.entries)-1]
}

func (t *activeRealtimeTranscript) appendDelta(role string, delta string, forceNew bool, frameless bool) {
	if delta == "" {
		return
	}
	if entry := t.findEntry(role, frameless); !forceNew && entry != nil {
		entry.Text += delta
		return
	}
	t.entries = append(t.entries, TranscriptEntry{Role: role, Text: delta})
}

func (t *activeRealtimeTranscript) applyDone(role string, text string, forceNew bool, frameless bool) {
	if text == "" {
		return
	}
	if entry := t.findEntry(role, frameless); !forceNew && entry != nil {
		// A delayed V3 final can arrive after deltas from the next utterance.
		// Preserve accumulated speech unless the final extends it.
		if !frameless || strings.HasPrefix(text, entry.Text) {
			entry.Text = text
		}
		return
	}
	t.entries = append(t.entries, TranscriptEntry{Role: role, Text: text})
}

func (t *activeRealtimeTranscript) appendHandoffInput(input string) {
	if t == nil {
		return
	}
	input = strings.TrimSpace(input)
	if input == "" || t.containsEntry("user", input) {
		return
	}
	if len(t.flushedUserInputs) > 0 {
		if _, flushed := t.flushedUserInputs[realtimeTranscriptHash(input)]; flushed {
			return
		}
	}
	t.entries = append(t.entries, TranscriptEntry{Role: "user", Text: input})
}

func (t *activeRealtimeTranscript) containsEntry(role string, text string) bool {
	text = strings.TrimSpace(text)
	for _, entry := range t.entries {
		if entry.Role == role && strings.TrimSpace(entry.Text) == text {
			return true
		}
	}
	return false
}

func (t *activeRealtimeTranscript) plainEntries() []TranscriptEntry {
	if t == nil {
		return nil
	}
	return append([]TranscriptEntry(nil), t.entries...)
}

func (t *activeRealtimeTranscript) takeEntries() []TranscriptEntry {
	if t == nil {
		return nil
	}
	entries := t.entries
	t.entries = nil
	t.flushedUserInputs = nil
	return entries
}

// realtimeTranscriptEntryBytes mirrors Rust's transcript_entry_bytes: the
// rendered "role: text" line plus its separator.
func realtimeTranscriptEntryBytes(entry TranscriptEntry) int {
	return len(entry.Role) + len(entry.Text) + 3
}

func realtimeTranscriptEntriesBytes(entries []TranscriptEntry) int {
	total := 0
	for _, entry := range entries {
		total += realtimeTranscriptEntryBytes(entry)
	}
	return total
}

func realtimeTranscriptHash(text string) uint64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(text))
	return hasher.Sum64()
}

// takeCompletedTranscript mirrors Rust's take_completed_transcript (#58221):
// after one speaker finishes a turn, the other speaker can still have an open
// utterance. Once the active transcript exceeds threshold, everything before
// that utterance is returned (in conversation order) for history recording, and
// its user inputs are remembered so a later echoed handoff is not duplicated.
func (t *activeRealtimeTranscript) takeCompletedTranscript(
	eventType string,
	newInputEntry bool,
	newOutputEntry bool,
	threshold int,
) []TranscriptEntry {
	if t == nil {
		return nil
	}
	openRole := ""
	switch eventType {
	case "input_transcript.done":
		if !newOutputEntry {
			openRole = "assistant"
		}
	case "output_transcript.done":
		if !newInputEntry {
			openRole = "user"
		}
	default:
		return nil
	}
	end := len(t.entries)
	if openRole != "" {
		for index := len(t.entries) - 1; index >= 0; index-- {
			if t.entries[index].Role == openRole {
				end = index
				break
			}
		}
	}
	if end == 0 || realtimeTranscriptEntriesBytes(t.entries) <= threshold {
		return nil
	}
	completed := append([]TranscriptEntry(nil), t.entries[:end]...)
	t.entries = append([]TranscriptEntry(nil), t.entries[end:]...)
	if t.flushedUserInputs == nil {
		t.flushedUserInputs = map[uint64]struct{}{}
	}
	for _, entry := range completed {
		if entry.Role == "user" {
			t.flushedUserInputs[realtimeTranscriptHash(strings.TrimSpace(entry.Text))] = struct{}{}
		}
	}
	return completed
}

func (t *activeRealtimeTranscript) takeHandoff(input string) []TranscriptEntry {
	if t == nil {
		return nil
	}
	t.appendHandoffInput(input)
	return t.takeEntries()
}

func (s *realtimeTransportSession) takeTranscriptTail() []TranscriptEntry {
	if s == nil || !s.config.FlushTranscriptTailOnEnd {
		return nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.tailFlushed {
		return nil
	}
	s.tailFlushed = true
	return s.transcript.takeEntries()
}

func (m *Manager) flushRealtimeTranscriptTail(connection *realtimeTransportSession) {
	if m == nil || connection == nil {
		return
	}
	tail := connection.takeTranscriptTail()
	if len(tail) == 0 {
		return
	}
	m.mu.Lock()
	eventSink := m.eventSink
	m.mu.Unlock()
	if eventSink != nil {
		eventSink(connection.threadID, Event{Type: "transcript_tail.flush", ActiveTranscript: tail})
	}
}

func (m *Manager) handleRealtimeTransportEvent(connection *realtimeTransportSession, event Event) {
	if m == nil || connection == nil {
		return
	}
	m.mu.Lock()
	active := m.connections[connection.threadID] == connection
	sink := m.notificationSink
	eventSink := m.eventSink
	m.mu.Unlock()
	if !active {
		return
	}
	if eventSink != nil {
		eventSink(connection.threadID, event)
	}
	if notification, ok := NotificationFromEvent(connection.threadID, event); ok && sink != nil {
		sink(notification)
	}
	for _, itemNotification := range m.observeTimelineEvent(connection.threadID, event) {
		if sink != nil {
			sink(itemNotification)
		}
	}
}

func (m *Manager) finishRealtimeTransport(connection *realtimeTransportSession, reason string, transportErr error) {
	if m == nil || connection == nil {
		return
	}
	m.mu.Lock()
	if m.connections[connection.threadID] != connection {
		m.mu.Unlock()
		return
	}
	delete(m.connections, connection.threadID)
	delete(m.sidebands, connection.threadID)
	state := m.sessions[connection.threadID]
	if state == nil || state.ClosedAt != nil {
		m.mu.Unlock()
		return
	}
	now := m.now().UTC()
	state.ClosedAt = &now
	state.LastActivity = now
	state.CloseReason = reason
	sink := m.notificationSink
	m.mu.Unlock()
	if sink != nil && transportErr != nil {
		sink(Notification{Method: NotificationError, Params: ErrorNotification{ThreadID: connection.threadID, Message: transportErr.Error()}})
	}
	// The timeline closes before the session reports itself closed, mirroring
	// the order where the closed event seals any remaining segments.
	itemNotifications := m.takeSessionClosedNotifications(connection.threadID)
	if sink != nil {
		for _, itemNotification := range itemNotifications {
			sink(itemNotification)
		}
		sink(NewClosedNotification(connection.threadID, reason))
	}
}

func realtimeSidebandURL(base, callID string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid realtime sideband base URL %q", base)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported realtime sideband URL scheme %q", parsed.Scheme)
	}
	cleaned := strings.TrimSuffix(parsed.Path, "/")
	switch {
	case cleaned == "" || cleaned == "/":
		parsed.Path = "/v1/realtime"
	case strings.HasSuffix(cleaned, "/realtime"):
		parsed.Path = cleaned
	case strings.HasSuffix(cleaned, "/v1"):
		parsed.Path = cleaned + "/realtime"
	default:
		parsed.Path = cleaned + "/realtime"
	}
	query := parsed.Query()
	query.Set("call_id", callID)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func (m *Manager) notifySidebandError(sideband *realtimeSideband, err error) {
	if m == nil || sideband == nil || err == nil || sideband.ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	if m.sidebands[sideband.threadID] != sideband || sideband.ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	delete(m.sidebands, sideband.threadID)
	state := m.sessions[sideband.threadID]
	if state == nil || state.ClosedAt != nil {
		m.mu.Unlock()
		return
	}
	now := m.now().UTC()
	state.ClosedAt = &now
	state.LastActivity = now
	state.CloseReason = "error"
	sink := m.notificationSink
	m.mu.Unlock()
	if sink != nil {
		sink(Notification{Method: NotificationError, Params: ErrorNotification{ThreadID: sideband.threadID, Message: err.Error()}})
		sink(NewClosedNotification(sideband.threadID, "error"))
	}
}
