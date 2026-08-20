package realtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const realtimeMultipartBoundary = "codex-realtime-call-boundary"

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
	backend  *TransportBackendConfig
	config   *SessionConfig
	ctx      context.Context
	cancel   context.CancelFunc
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
	transcript            []TranscriptEntry
	lastHandoffEntryCount int
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
		// Rust #39257: reconnect frameless WebRTC sideband sockets after
		// unexpected transport loss with capped exponential backoff, while a
		// terminal handshake status (404/410) completes the session.
		attempt := 0
		for {
			connection, err := dialRealtimeTransport(sideband.ctx, sideband.threadID, sideband.backend, sideband.config, sideband.callID, false)
			if err != nil {
				if realtimeHandshakeTerminal(err) {
					return
				}
				if attempt == 0 {
					m.notifySidebandError(sideband, fmt.Errorf("connect realtime sideband: %w", err))
				}
				attempt++
				if sideband.ctx.Err() != nil || !m.sidebandShouldReconnect(sideband) || attempt > realtimeSidebandMaxReconnectAttempts {
					return
				}
				if !sleepSidebandReconnect(sideband.ctx, realtimeSidebandReconnectDelay(attempt)) {
					return
				}
				continue
			}
			m.mu.Lock()
			state := m.sessions[sideband.threadID]
			if m.shutdown || sideband.ctx.Err() != nil || m.sidebands[sideband.threadID] != sideband || state == nil || state.ClosedAt != nil {
				m.mu.Unlock()
				connection.closeNow()
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
			attempt++
			if attempt > realtimeSidebandMaxReconnectAttempts {
				return
			}
			if !sleepSidebandReconnect(sideband.ctx, realtimeSidebandReconnectDelay(attempt)) {
				return
			}
		}
	}()
}

const realtimeSidebandMaxReconnectAttempts = 5

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
	delay := time.Duration(250*(1<<min(attempt-1, 4))) * time.Millisecond
	if delay > 5*time.Second {
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

func realtimeHandshakeTerminal(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "404") || strings.Contains(text, "410") ||
		strings.Contains(text, "not found") || strings.Contains(text, "gone")
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
	defer connection.closeNow()
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
		s.transcript = appendRealtimeTranscriptDelta(s.transcript, "user", event.Delta, s.newInputEntry)
		s.newInputEntry = false
	case "output_transcript.delta":
		s.transcript = appendRealtimeTranscriptDelta(s.transcript, "assistant", event.Delta, s.newOutputEntry)
		s.newOutputEntry = false
	case "input_transcript.done":
		s.transcript = applyRealtimeTranscriptDone(s.transcript, "user", event.Text, s.newInputEntry)
		s.newInputEntry = false
	case "output_transcript.done":
		s.transcript = applyRealtimeTranscriptDone(s.transcript, "assistant", event.Text, s.newOutputEntry)
		s.newOutputEntry = false
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
		s.transcript = appendRealtimeHandoffInput(s.transcript, event.InputTranscript)
		event.ActiveTranscript = append([]TranscriptEntry(nil), s.transcript[s.lastHandoffEntryCount:]...)
		s.lastHandoffEntryCount = len(s.transcript)
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

func appendRealtimeTranscriptDelta(entries []TranscriptEntry, role, delta string, forceNew bool) []TranscriptEntry {
	if delta == "" {
		return entries
	}
	if !forceNew && len(entries) > 0 && entries[len(entries)-1].Role == role {
		entries[len(entries)-1].Text += delta
		return entries
	}
	return append(entries, TranscriptEntry{Role: role, Text: delta})
}

func applyRealtimeTranscriptDone(entries []TranscriptEntry, role, value string, forceNew bool) []TranscriptEntry {
	if value == "" {
		return entries
	}
	if !forceNew && len(entries) > 0 && entries[len(entries)-1].Role == role {
		entries[len(entries)-1].Text = value
		return entries
	}
	return append(entries, TranscriptEntry{Role: role, Text: value})
}

func appendRealtimeHandoffInput(entries []TranscriptEntry, input string) []TranscriptEntry {
	input = strings.TrimSpace(input)
	if input == "" {
		return entries
	}
	for _, entry := range entries {
		if entry.Role == "user" && strings.TrimSpace(entry.Text) == input {
			return entries
		}
	}
	return append(entries, TranscriptEntry{Role: "user", Text: input})
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
	tail := append([]TranscriptEntry(nil), s.transcript[s.lastHandoffEntryCount:]...)
	s.lastHandoffEntryCount = len(s.transcript)
	return tail
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
	if sink != nil {
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
