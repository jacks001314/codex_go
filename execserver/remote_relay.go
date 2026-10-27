package execserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"codex_go/network"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	clatter "github.com/shurlinet/go-clatter"
)

const (
	remoteRelayChannelCapacity             = 128
	maxActiveNoiseRelayStreams             = 128
	maxFailedNoiseHandshakes               = 8
	maxHarnessKeyAuthorizationBytes        = 4096
	maxPendingHandshakeValidations         = 32
	harnessKeyValidationTimeout            = 10 * time.Second
	remoteRelayKeepaliveInterval           = 30 * time.Second
	noiseRecordPlaintextLen                = 60 * 1024
	maxNoiseJSONRPCMessageLen              = 64 * 1024 * 1024
	maxNoiseRelayReorderDistance    uint32 = 64
	maxNoiseRelayPendingBytes              = 1024 * 1024
	// maxHandshakeDiagnosticsPerMinute mirrors Rust's
	// MAX_HANDSHAKE_DIAGNOSTICS_PER_MINUTE (#55498): one relay connection may
	// emit this many correlated handshake diagnostics per window, followed by a
	// single suppression marker.
	maxHandshakeDiagnosticsPerMinute = 100
)

// noiseHandshakeFailureCooldown mirrors Rust's
// NOISE_HANDSHAKE_FAILURE_COOLDOWN (#46031): once the handshake failure budget is
// exhausted the relay pauses new handshake admission for this window instead of
// closing, so authenticated streams stay connected. It is a variable so tests
// can shorten the window instead of waiting it out.
var noiseHandshakeFailureCooldown = 10 * time.Second

// handshakeDiagnosticWindow mirrors Rust's HANDSHAKE_DIAGNOSTIC_WINDOW. It is a
// variable so tests can advance the window without waiting a minute.
var handshakeDiagnosticWindow = 60 * time.Second

// recordFailedHandshake mirrors Rust's `record_failed_handshake`: exhausting the
// failure budget starts the cooldown, and failures recorded during the cooldown
// do not extend it.
func recordFailedHandshake(failedHandshakes *int, cooldownUntil *time.Time) {
	if failedHandshakes == nil || cooldownUntil == nil || !cooldownUntil.IsZero() {
		return
	}
	*failedHandshakes++
	if *failedHandshakes >= maxFailedNoiseHandshakes {
		*cooldownUntil = time.Now().Add(noiseHandshakeFailureCooldown)
		slog.Warn("pausing Noise relay handshakes after repeated failures")
	}
}

// handshakeDiagnosticSlot returns the next slot in this window. The limit slot
// records a suppression marker; all later slots suppress diagnostics until the
// window resets (Rust #55498 `handshake_diagnostic_slot`).
func handshakeDiagnosticSlot(windowStart *time.Time, count *int, now time.Time) int {
	if windowStart == nil || count == nil {
		return maxHandshakeDiagnosticsPerMinute + 1
	}
	if now.Sub(*windowStart) >= handshakeDiagnosticWindow {
		*windowStart = now
		*count = 0
	}
	slot := *count
	*count = min(slot+1, maxHandshakeDiagnosticsPerMinute+1)
	return slot
}

// handshakeDiagnostics rate limits correlated diagnostics for one physical
// relay connection (Rust #55498).
type handshakeDiagnostics struct {
	environmentID          string
	executorRegistrationID string
	windowStart            time.Time
	count                  int
}

func newHandshakeDiagnostics(environmentID, executorRegistrationID string) *handshakeDiagnostics {
	return &handshakeDiagnostics{
		environmentID:          environmentID,
		executorRegistrationID: executorRegistrationID,
		windowStart:            time.Now(),
	}
}

// record emits one handshake diagnostic unless this connection's window is
// exhausted, in which case the first suppressed call reports a rate-limit
// marker and every later call stays silent.
func (d *handshakeDiagnostics) record(streamID, reason string, registryStatus *int) {
	if d == nil {
		return
	}
	switch slot := handshakeDiagnosticSlot(&d.windowStart, &d.count, time.Now()); {
	case slot < maxHandshakeDiagnosticsPerMinute:
		recordHandshakeDiagnostic(d.environmentID, d.executorRegistrationID, streamID, reason, registryStatus)
	case slot == maxHandshakeDiagnosticsPerMinute:
		slog.Warn("Noise executor handshake diagnostics rate limited",
			"noise_event", "handshake_diagnostics",
			"noise_outcome", "rate_limited",
			"noise_side", "executor",
			"environment_id", d.environmentID,
			"executor_registration_id", d.executorRegistrationID,
		)
	}
}

// recordHandshakeDiagnostic mirrors Rust #55498's
// `record_handshake_diagnostic`: bounded, non-secret context for an unfinished
// handshake. The peer controls stream_id, so unvalidated wire data is never
// exported and non-UUID values collapse to "invalid".
func recordHandshakeDiagnostic(environmentID, executorRegistrationID, streamID, reason string, registryStatus *int) {
	normalizedStreamID := "invalid"
	if parsed, err := uuid.Parse(streamID); err == nil {
		normalizedStreamID = parsed.String()
	}
	outcome := "rejected"
	if reason == "reset_pending_validation" {
		outcome = "reset_received"
	}
	attributes := []any{
		"noise_event", "handshake",
		"noise_outcome", outcome,
		"noise_side", "executor",
		"noise_reason", reason,
		"environment_id", environmentID,
		"executor_registration_id", executorRegistrationID,
		"stream_id", normalizedStreamID,
	}
	if registryStatus != nil {
		attributes = append(attributes, "registry_status", *registryStatus)
	}
	slog.Warn("Noise executor handshake diagnostic", attributes...)
}

// classifyHarnessKeyValidationError maps a harness key validation failure onto
// Rust's closed set of diagnostic reasons (#55498). Error text is never
// exported: only the category and, when available, the registry HTTP status.
func classifyHarnessKeyValidationError(err error) (string, *int) {
	if err == nil {
		return "", nil
	}
	if network.IsPolicyError(err) {
		return "network_policy", nil
	}
	var registryErr *remoteRegistryHTTPError
	if errors.As(err, &registryErr) {
		status := registryErr.StatusCode
		return "registry_http", &status
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "environment registry authentication error"),
		strings.Contains(message, "environment registry auth resolution failed"):
		return "registry_auth", nil
	case strings.Contains(message, "environment registry config error"):
		return "registry_config", nil
	case strings.Contains(message, "environment registry request failed"):
		if isRegistryRequestTimeout(err) {
			return "registry_request_timeout", nil
		}
		return "registry_request", nil
	case strings.Contains(message, "exec-server protocol error"):
		return "validation_protocol", nil
	}
	return "validation_error", nil
}

func isRegistryRequestTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

type remoteHarnessValidationRequest struct {
	ExecutorRegistrationID  string          `json:"executor_registration_id"`
	HarnessPublicKey        RemotePublicKey `json:"harness_public_key"`
	HarnessKeyAuthorization string          `json:"harness_key_authorization"`
}

type remoteHarnessValidationResponse struct {
	Valid bool `json:"valid"`
}

type remoteRelayIncoming struct {
	frame relayMessageFrame
	err   error
}

type remoteRelayValidationResult struct {
	streamID       string
	validationID   uint64
	err            error
	reason         string
	registryStatus *int
}

type pendingRemoteRelayHandshake struct {
	validationID uint64
	handshake    *pendingRemoteNoiseHandshake
}

type remoteRelayClosedStream struct {
	streamID   string
	instanceID uint64
}

func (s *Server) serveNoiseRelayConnection(
	ctx context.Context,
	conn *websocket.Conn,
	cfg RemoteEnvironmentConfig,
	registration *remoteRegistrationResponse,
	identity *remoteNoiseIdentity,
) error {
	if conn == nil || registration == nil || identity == nil {
		return errors.New("Noise relay connection is incomplete")
	}
	conn.SetReadLimit(256 * 1024)
	connectionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.CloseNow()

	outgoing := make(chan []byte, remoteRelayChannelCapacity)
	incoming := make(chan remoteRelayIncoming, remoteRelayChannelCapacity)
	writerDone := make(chan error, 1)
	validationDone := make(chan remoteRelayValidationResult, maxPendingHandshakeValidations)
	closedStreams := make(chan remoteRelayClosedStream, maxActiveNoiseRelayStreams)

	go runRemoteRelayWriter(connectionCtx, conn, outgoing, writerDone)
	go runRemoteRelayReader(connectionCtx, conn, incoming)

	streams := map[string]*remoteNoiseVirtualStream{}
	pending := map[string]pendingRemoteRelayHandshake{}
	defer func() {
		for _, stream := range streams {
			stream.Close()
		}
		for _, item := range pending {
			item.handshake.Destroy()
		}
	}()

	failedHandshakes := 0
	var handshakeCooldownUntil time.Time
	var nextValidationID uint64
	diagnostics := newHandshakeDiagnostics(registration.EnvironmentID, registration.ExecutorRegistrationID)
	for {
		select {
		case <-connectionCtx.Done():
			return nil
		case err := <-writerDone:
			if err == nil || ctx.Err() != nil {
				return nil
			}
			return err
		case closed := <-closedStreams:
			if stream := streams[closed.streamID]; stream != nil && stream.instanceID == closed.instanceID {
				delete(streams, closed.streamID)
				stream.Close()
			}
		case result := <-validationDone:
			item, exists := pending[result.streamID]
			if !exists || item.validationID != result.validationID {
				continue
			}
			delete(pending, result.streamID)
			if result.err != nil {
				item.handshake.Destroy()
				diagnostics.record(result.streamID, result.reason, result.registryStatus)
				queueRelayReset(outgoing, result.streamID)
				recordFailedHandshake(&failedHandshakes, &handshakeCooldownUntil)
				continue
			}
			if len(streams) >= maxActiveNoiseRelayStreams {
				item.handshake.Destroy()
				diagnostics.record(result.streamID, "active_stream_limit", nil)
				queueRelayReset(outgoing, result.streamID)
				continue
			}
			transport, response, err := item.handshake.Complete()
			if err != nil {
				diagnostics.record(result.streamID, "handshake_completion", nil)
				queueRelayReset(outgoing, result.streamID)
				recordFailedHandshake(&failedHandshakes, &handshakeCooldownUntil)
				continue
			}
			if err := queueRelayFrame(outgoing, newRelayHandshakeFrame(result.streamID, response)); err != nil {
				diagnostics.record(result.streamID, "response_queue_full", nil)
				transport.Destroy()
				return err
			}
			streams[result.streamID] = newRemoteNoiseVirtualStream(
				connectionCtx,
				s,
				result.streamID,
				result.validationID,
				transport,
				outgoing,
				closedStreams,
			)
		case event := <-incoming:
			if event.err != nil {
				if ctx.Err() != nil || websocket.CloseStatus(event.err) != -1 {
					return nil
				}
				return event.err
			}
			frame := event.frame
			switch frame.Kind {
			case relayFrameHandshake:
				// Rust #46031: stop admitting work before parsing another hybrid
				// handshake. Existing streams and already-admitted validations keep
				// running, and the failure budget resets once admission resumes.
				if !handshakeCooldownUntil.IsZero() {
					if time.Now().Before(handshakeCooldownUntil) {
						diagnostics.record(frame.StreamID, "cooldown", nil)
						queueRelayReset(outgoing, frame.StreamID)
						continue
					}
					failedHandshakes = 0
					handshakeCooldownUntil = time.Time{}
				}
				if _, exists := streams[frame.StreamID]; exists {
					diagnostics.record(frame.StreamID, "duplicate_active_stream", nil)
					queueRelayReset(outgoing, frame.StreamID)
					continue
				}
				if previous, exists := pending[frame.StreamID]; exists {
					delete(pending, frame.StreamID)
					previous.handshake.Destroy()
					diagnostics.record(frame.StreamID, "duplicate_pending_stream", nil)
					queueRelayReset(outgoing, frame.StreamID)
					recordFailedHandshake(&failedHandshakes, &handshakeCooldownUntil)
					continue
				}
				if len(streams) >= maxActiveNoiseRelayStreams {
					diagnostics.record(frame.StreamID, "active_stream_limit", nil)
					queueRelayReset(outgoing, frame.StreamID)
					continue
				}
				if len(pending) >= maxPendingHandshakeValidations {
					diagnostics.record(frame.StreamID, "pending_validation_limit", nil)
					queueRelayReset(outgoing, frame.StreamID)
					continue
				}
				prologue := noiseChannelPrologue(registration.EnvironmentID, registration.ExecutorRegistrationID, frame.StreamID)
				handshake, authorization, err := readRemoteNoiseHandshake(identity, prologue, frame.HandshakeBytes)
				if err != nil {
					if handshake != nil {
						handshake.Destroy()
					}
					diagnostics.record(frame.StreamID, "handshake_request", nil)
					queueRelayReset(outgoing, frame.StreamID)
					recordFailedHandshake(&failedHandshakes, &handshakeCooldownUntil)
					continue
				}
				if len(authorization) > maxHarnessKeyAuthorizationBytes {
					handshake.Destroy()
					diagnostics.record(frame.StreamID, "authorization_too_long", nil)
					queueRelayReset(outgoing, frame.StreamID)
					continue
				}
				if !utf8.ValidString(authorization) {
					handshake.Destroy()
					diagnostics.record(frame.StreamID, "authorization_not_utf8", nil)
					queueRelayReset(outgoing, frame.StreamID)
					continue
				}
				validationID := nextValidationID
				nextValidationID++
				pending[frame.StreamID] = pendingRemoteRelayHandshake{validationID: validationID, handshake: handshake}
				go func(streamID string, publicKey RemotePublicKey, authorization string, validationID uint64) {
					validationCtx, validationCancel := context.WithTimeout(connectionCtx, harnessKeyValidationTimeout)
					defer validationCancel()
					err := validateRemoteHarnessKey(validationCtx, cfg, registration, publicKey, authorization, streamID)
					result := remoteRelayValidationResult{streamID: streamID, validationID: validationID, err: err}
					if err != nil {
						if validationCtx.Err() != nil {
							result.reason = "validation_timeout"
						} else {
							result.reason, result.registryStatus = classifyHarnessKeyValidationError(err)
						}
					}
					select {
					case validationDone <- result:
					case <-connectionCtx.Done():
					}
				}(frame.StreamID, handshake.publicKey, authorization, validationID)
			case relayFrameData:
				stream := streams[frame.StreamID]
				if stream == nil {
					if item, exists := pending[frame.StreamID]; exists {
						delete(pending, frame.StreamID)
						item.handshake.Destroy()
						diagnostics.record(frame.StreamID, "data_before_handshake", nil)
						recordFailedHandshake(&failedHandshakes, &handshakeCooldownUntil)
					}
					queueRelayReset(outgoing, frame.StreamID)
					continue
				}
				if err := stream.Receive(frame.Data); err != nil {
					delete(streams, frame.StreamID)
					stream.Close()
					queueRelayReset(outgoing, frame.StreamID)
				}
			case relayFrameReset:
				if item, exists := pending[frame.StreamID]; exists {
					delete(pending, frame.StreamID)
					item.handshake.Destroy()
					// A reset may come from the harness or the relay; its reason is
					// untrusted, so only the correlated stream id is reported.
					diagnostics.record(frame.StreamID, "reset_pending_validation", nil)
				}
				if stream := streams[frame.StreamID]; stream != nil {
					delete(streams, frame.StreamID)
					stream.Close()
				}
			case relayFrameAck, relayFrameResume, relayFrameHeartbeat:
			}
		}
	}
}

// remoteRelayWriteTimeout bounds a single relayed data write. Rust #51502
// (relay_writer.rs, WEBSOCKET_PONG_TIMEOUT = 60s) gives each write a fixed
// budget independent of the pong deadline, so a peer that stops reading cannot
// pin the relay writer forever.
const remoteRelayWriteTimeout = 60 * time.Second

func runRemoteRelayWriter(ctx context.Context, conn *websocket.Conn, outgoing <-chan []byte, done chan<- error) {
	ticker := time.NewTicker(remoteRelayKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			done <- nil
			return
		case payload := <-outgoing:
			// The write budget is independent of pong deadlines: a pong may clear
			// its own deadline, never the in-flight write.
			writeCtx, cancelWrite := context.WithTimeout(ctx, remoteRelayWriteTimeout)
			err := conn.Write(writeCtx, websocket.MessageBinary, payload)
			cancelWrite()
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) {
					done <- errors.New("Noise multiplexed environment websocket write timed out")
					return
				}
				done <- fmt.Errorf("Noise multiplexed environment websocket write failed: %w", err)
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				done <- fmt.Errorf("Noise multiplexed environment websocket keepalive failed: %w", err)
				return
			}
		}
	}
}

func runRemoteRelayReader(ctx context.Context, conn *websocket.Conn, incoming chan<- remoteRelayIncoming) {
	for {
		messageType, payload, err := conn.Read(ctx)
		if err != nil {
			select {
			case incoming <- remoteRelayIncoming{err: err}:
			case <-ctx.Done():
			}
			return
		}
		if messageType != websocket.MessageBinary {
			continue
		}
		frame, err := decodeRelayMessageFrame(payload)
		if err != nil {
			continue
		}
		select {
		case incoming <- remoteRelayIncoming{frame: frame}:
		case <-ctx.Done():
			return
		}
	}
}

func validateRemoteHarnessKey(
	ctx context.Context,
	cfg RemoteEnvironmentConfig,
	registration *remoteRegistrationResponse,
	publicKey RemotePublicKey,
	authorization string,
	streamID string,
) error {
	body, err := json.Marshal(remoteHarnessValidationRequest{
		ExecutorRegistrationID:  registration.ExecutorRegistrationID,
		HarnessPublicKey:        publicKey,
		HarnessKeyAuthorization: authorization,
	})
	if err != nil {
		return err
	}
	// Rust #54346 routes this through `resolve_auth` + `http_for_auth`, so the
	// scoped mode validates its single bearer here too and uses the
	// proxy-bypassing client for a loopback HTTP registry. Rust #55987 also
	// replays the validation once after a 401 with replacement credentials.
	build := func() (*http.Request, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, remoteEndpointURL(cfg.BaseURL, "/cloud/environment/"+cfg.EnvironmentID+"/validate"), bytes.NewReader(body))
		if err != nil {
			return nil, errors.New("environment registry harness key validation failed")
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
		// Rust #55498 forwards only a valid stream UUID so the registry can
		// correlate its validation with the relay's handshake diagnostics.
		if parsed, parseErr := uuid.Parse(streamID); parseErr == nil {
			request.Header.Set("x-codex-noise-stream-id", parsed.String())
		}
		return request, nil
	}
	response, executorCredential, err := sendRegistryAuthenticated(ctx, cfg, build)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// The request carries the short-lived authorization, so the body is
		// never read. The typed status still classifies the diagnostic.
		return &remoteRegistryHTTPError{
			StatusCode: response.StatusCode,
			message:    fmt.Sprintf("environment registry harness key validation failed (%s)", response.Status),
		}
	}
	var decoded remoteHarnessValidationResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&decoded); err != nil {
		if executorCredential != "" {
			// The body could echo the credential, so only fixed text is used.
			return errors.New("exec-server protocol error: invalid environment registry harness key validation response")
		}
		return errors.New("environment registry harness key validation failed")
	}
	if !decoded.Valid {
		return errors.New("environment registry rejected Noise relay harness key")
	}
	return nil
}

func noiseChannelPrologue(environmentID string, registrationID string, streamID string) []byte {
	parts := [][]byte{
		[]byte("codex-exec-server-relay-noise/v1"),
		[]byte(environmentID),
		[]byte(registrationID),
		[]byte(streamID),
	}
	var prologue []byte
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		prologue = append(prologue, length[:]...)
		prologue = append(prologue, part...)
	}
	return prologue
}

func queueRelayReset(outgoing chan<- []byte, streamID string) {
	encoded, err := encodeRelayMessageFrame(newRelayResetFrame(streamID))
	if err != nil {
		return
	}
	select {
	case outgoing <- encoded:
	default:
	}
}

func queueRelayFrame(outgoing chan<- []byte, frame relayMessageFrame) error {
	encoded, err := encodeRelayMessageFrame(frame)
	if err != nil {
		return err
	}
	select {
	case outgoing <- encoded:
		return nil
	default:
		return errors.New("Noise relay outgoing queue is full")
	}
}

type remoteNoiseVirtualStream struct {
	streamID    string
	instanceID  uint64
	transport   *clatter.TransportState
	transportMu sync.RWMutex
	conn        net.Conn
	incoming    chan []byte
	cancel      context.CancelFunc
	closeOnce   sync.Once
	reorder     remoteOrderedCiphertexts
	decoder     remoteJSONRPCDecoder
}

func newRemoteNoiseVirtualStream(
	ctx context.Context,
	server *Server,
	streamID string,
	instanceID uint64,
	transport *clatter.TransportState,
	outgoing chan<- []byte,
	closed chan<- remoteRelayClosedStream,
) *remoteNoiseVirtualStream {
	streamCtx, cancel := context.WithCancel(ctx)
	clientConn, serverConn := net.Pipe()
	stream := &remoteNoiseVirtualStream{
		streamID: streamID, instanceID: instanceID, transport: transport,
		conn: clientConn, incoming: make(chan []byte, remoteRelayChannelCapacity), cancel: cancel,
	}
	go func() {
		defer serverConn.Close()
		// Rust #39235: when the JSON-RPC processor exits before the writer
		// task, the physical relay still resets the corresponding harness
		// stream so the peer does not wait on a dead connection.
		_ = server.serveConnectionStream(streamCtx, serverConn, serverConn)
		queueRelayReset(outgoing, streamID)
	}()
	go stream.runInboundWriter(streamCtx)
	go stream.runOutboundReader(streamCtx, outgoing, closed)
	return stream
}

func (s *remoteNoiseVirtualStream) Receive(data relayData) error {
	frames, err := s.reorder.Push(data.Seq, data.Payload)
	if err != nil {
		return err
	}
	for _, ciphertext := range frames {
		if len(ciphertext) < clatter.TagLen {
			return errors.New("Noise relay ciphertext is too short")
		}
		plaintext := make([]byte, len(ciphertext)-clatter.TagLen)
		s.transportMu.RLock()
		length, err := s.transport.Receive(ciphertext, plaintext)
		s.transportMu.RUnlock()
		if err != nil {
			return fmt.Errorf("Noise relay decryption failed: %w", err)
		}
		messages, err := s.decoder.Push(plaintext[:length])
		if err != nil {
			return err
		}
		for _, message := range messages {
			select {
			case s.incoming <- message:
			default:
				return errors.New("Noise virtual stream inbound queue is full or closed")
			}
		}
	}
	return nil
}

func (s *remoteNoiseVirtualStream) runInboundWriter(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-s.incoming:
			message = append(message, '\n')
			if _, err := s.conn.Write(message); err != nil {
				s.Close()
				return
			}
		}
	}
}

func (s *remoteNoiseVirtualStream) runOutboundReader(ctx context.Context, outgoing chan<- []byte, closed chan<- remoteRelayClosedStream) {
	defer func() {
		select {
		case closed <- remoteRelayClosedStream{streamID: s.streamID, instanceID: s.instanceID}:
		case <-ctx.Done():
		}
	}()
	reader := bufio.NewReader(s.conn)
	var nextSeq uint32
	for {
		message, err := readRemoteRelayJSONLine(reader)
		if err != nil {
			return
		}
		framed := make([]byte, 4, len(message)+4)
		binary.BigEndian.PutUint32(framed, uint32(len(message)))
		framed = append(framed, message...)
		for len(framed) > 0 {
			chunkLen := min(len(framed), noiseRecordPlaintextLen)
			ciphertext := make([]byte, chunkLen+clatter.TagLen)
			s.transportMu.RLock()
			length, err := s.transport.Send(framed[:chunkLen], ciphertext)
			s.transportMu.RUnlock()
			if err != nil {
				return
			}
			if nextSeq == ^uint32(0) {
				return
			}
			if err := queueRelayFrame(outgoing, newRelayDataFrame(s.streamID, nextSeq, ciphertext[:length])); err != nil {
				return
			}
			nextSeq++
			framed = framed[chunkLen:]
		}
	}
}

func (s *remoteNoiseVirtualStream) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.conn.Close()
		s.transportMu.Lock()
		s.transport.Destroy()
		s.transportMu.Unlock()
	})
}

func readRemoteRelayJSONLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		line = append(line, fragment...)
		if len(line) > maxNoiseJSONRPCMessageLen+1 {
			return nil, errors.New("Noise relay JSON-RPC message exceeds maximum length")
		}
		if err == nil {
			line = line[:len(line)-1]
			if len(line) == 0 || !json.Valid(line) {
				return nil, errors.New("Noise relay JSON-RPC message is invalid")
			}
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}

type remoteJSONRPCDecoder struct {
	buffered []byte
}

func (d *remoteJSONRPCDecoder) Push(plaintext []byte) ([][]byte, error) {
	if len(plaintext) > noiseRecordPlaintextLen {
		return nil, errors.New("Noise relay plaintext record exceeds maximum length")
	}
	d.buffered = append(d.buffered, plaintext...)
	var messages [][]byte
	for len(d.buffered) >= 4 {
		messageLen := int(binary.BigEndian.Uint32(d.buffered[:4]))
		if messageLen == 0 || messageLen > maxNoiseJSONRPCMessageLen {
			return nil, errors.New("Noise relay JSON-RPC message has invalid length")
		}
		if len(d.buffered) < 4+messageLen {
			break
		}
		message := append([]byte(nil), d.buffered[4:4+messageLen]...)
		if !json.Valid(message) {
			return nil, errors.New("Noise relay JSON-RPC message is invalid")
		}
		messages = append(messages, message)
		d.buffered = d.buffered[4+messageLen:]
	}
	if len(d.buffered) > 4+maxNoiseJSONRPCMessageLen {
		return nil, errors.New("Noise relay JSON-RPC reassembly buffer exceeds maximum length")
	}
	return messages, nil
}

type remoteOrderedCiphertexts struct {
	nextSeq      uint32
	pending      map[uint32][]byte
	pendingBytes int
}

func (o *remoteOrderedCiphertexts) Push(seq uint32, payload []byte) ([][]byte, error) {
	if seq < o.nextSeq {
		return nil, nil
	}
	if o.pending == nil {
		o.pending = map[uint32][]byte{}
	}
	if _, exists := o.pending[seq]; exists {
		return nil, nil
	}
	if seq > o.nextSeq {
		if seq-o.nextSeq > maxNoiseRelayReorderDistance {
			return nil, errors.New("Noise relay ciphertext exceeds reorder window")
		}
		if o.pendingBytes+len(payload) > maxNoiseRelayPendingBytes {
			return nil, errors.New("Noise relay pending ciphertext buffer is full")
		}
		o.pending[seq] = append([]byte(nil), payload...)
		o.pendingBytes += len(payload)
		return nil, nil
	}
	ready := [][]byte{append([]byte(nil), payload...)}
	if o.nextSeq == ^uint32(0) {
		return nil, errors.New("Noise relay sequence number exhausted")
	}
	o.nextSeq++
	for {
		payload, exists := o.pending[o.nextSeq]
		if !exists {
			break
		}
		delete(o.pending, o.nextSeq)
		o.pendingBytes -= len(payload)
		ready = append(ready, payload)
		if o.nextSeq == ^uint32(0) {
			return nil, errors.New("Noise relay sequence number exhausted")
		}
		o.nextSeq++
	}
	return ready, nil
}
