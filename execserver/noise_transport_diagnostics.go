package execserver

// Trace-safe Noise relay transport progress diagnostics.
//
// Rust parity: codex-rs/exec-server/src/noise_relay/harness_diagnostics.rs
// (#58025). Only fixed exit categories, local counters, queue depths and local
// timings leave this file: request methods, ids, payloads, peer-provided reset
// text and raw transport errors must never be exported.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type relayTransportExitReason string

const (
	relayExitPongTimeout           relayTransportExitReason = "pong_timeout"
	relayExitOutgoingClosed        relayTransportExitReason = "outgoing_closed"
	relayExitFraming               relayTransportExitReason = "framing"
	relayExitSequenceExhausted     relayTransportExitReason = "sequence_exhausted"
	relayExitEncryption            relayTransportExitReason = "encryption"
	relayExitWebsocketWriteTimeout relayTransportExitReason = "websocket_write_timeout"
	relayExitWebsocketWrite        relayTransportExitReason = "websocket_write"
	relayExitWebsocketEnded        relayTransportExitReason = "websocket_ended"
	relayExitInvalidFrame          relayTransportExitReason = "invalid_frame"
	relayExitApplicationDelivery   relayTransportExitReason = "application_delivery"
	relayExitInvalidData           relayTransportExitReason = "invalid_data"
	relayExitRelayReset            relayTransportExitReason = "relay_reset"
	relayExitWebsocketClose        relayTransportExitReason = "websocket_close"
	relayExitUnexpectedText        relayTransportExitReason = "unexpected_text"
	relayExitWebsocketRead         relayTransportExitReason = "websocket_read"
)

// relayWriteExitReason keeps a write timeout distinct from a transport error
// (Rust WebsocketWriteError::exit_reason).
func relayWriteExitReason(err error) relayTransportExitReason {
	if err == nil {
		return relayExitWebsocketWrite
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return relayExitWebsocketWriteTimeout
	}
	return relayExitWebsocketWrite
}

// relayReadExitReason classifies a websocket read failure without exporting the
// error text.
func relayReadExitReason(err error) string {
	if err == nil {
		return "none"
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return "close"
	case -1:
		return "transport"
	default:
		return "status"
	}
}

// relayTransportDiagnostics keeps bounded, trace-safe progress counters for one
// relay websocket. The writer, the reader and the handler that logs the closure
// all touch it, so every field is guarded by mu.
type relayTransportDiagnostics struct {
	mu sync.Mutex

	connectedAt time.Time
	lastReadAt  time.Time
	lastWriteAt time.Time

	writeStartedAt time.Time
	writeKind      string

	recordsWritten   uint64
	framesRead       uint64
	messagesDequeued uint64
	messagesWritten  uint64
	pingsWritten     uint64
	pongsReceived    uint64

	lastPingAt    time.Time
	readReason    string
	exitReason    relayTransportExitReason
	outgoingQueue chan []byte
	incomingQueue chan remoteRelayIncoming
}

func newRelayTransportDiagnostics(outgoing chan []byte, incoming chan remoteRelayIncoming) *relayTransportDiagnostics {
	now := time.Now()
	return &relayTransportDiagnostics{
		connectedAt: now, lastReadAt: now, lastWriteAt: now,
		outgoingQueue: outgoing, incomingQueue: incoming,
	}
}

func (d *relayTransportDiagnostics) setExitReason(reason relayTransportExitReason) {
	if d == nil || reason == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exitReason = reason
}

// exitReasonValue reports the recorded reason without exposing the guarded
// field to the handler that classifies a writer failure.
func (d *relayTransportDiagnostics) exitReasonValue() relayTransportExitReason {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exitReason
}

func (d *relayTransportDiagnostics) recordMessageDequeued() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.messagesDequeued++
}

func (d *relayTransportDiagnostics) recordWriteStart(kind string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeStartedAt = time.Now()
	d.writeKind = kind
}

func (d *relayTransportDiagnostics) recordWriteComplete(kind string, records int) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.writeStartedAt = time.Time{}
	d.writeKind = ""
	d.lastWriteAt = time.Now()
	d.messagesWritten++
	d.recordsWritten += uint64(records)
	if kind == "keepalive" {
		d.pingsWritten++
		d.lastPingAt = time.Now()
		d.logHeartbeatLocked("ping_written")
	}
}

func (d *relayTransportDiagnostics) recordPongReceived() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastPingAt.IsZero() {
		// Unsolicited pongs must not create unbounded telemetry volume.
		return
	}
	d.pongsReceived++
	d.logHeartbeatLocked("pong_received")
	d.lastPingAt = time.Time{}
}

// logHeartbeatLocked emits one heartbeat event; the caller holds mu.
func (d *relayTransportDiagnostics) logHeartbeatLocked(phase string) {
	slog.Info("Noise relay heartbeat",
		"noise_event", "heartbeat",
		"noise_side", "harness",
		"phase", phase,
		"pings_written", d.pingsWritten,
		"pongs_received", d.pongsReceived,
		"ping_elapsed_ms", elapsedMillis(d.lastPingAt),
	)
}

func (d *relayTransportDiagnostics) recordFrameRead() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.framesRead++
	d.lastReadAt = time.Now()
}

func (d *relayTransportDiagnostics) recordReadError(err error) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.readReason = relayReadExitReason(err)
}

func elapsedMillis(start time.Time) uint64 {
	if start.IsZero() {
		return 0
	}
	elapsed := time.Since(start)
	if elapsed < 0 {
		return 0
	}
	return uint64(elapsed / time.Millisecond)
}

// logClosure emits the single transport-closure summary. Fixed categories and
// local counters only.
func (d *relayTransportDiagnostics) logClosure() {
	if d == nil {
		return
	}
	d.mu.Lock()
	reason := d.exitReason
	readReason := d.readReason
	writePending := !d.writeStartedAt.IsZero()
	writeKind := "none"
	if writePending {
		writeKind = d.writeKind
	}
	writeWait := elapsedMillis(d.writeStartedAt)
	messagesDequeued := d.messagesDequeued
	messagesWritten := d.messagesWritten
	pingsWritten := d.pingsWritten
	pongsReceived := d.pongsReceived
	framesRead := d.framesRead
	recordsWritten := d.recordsWritten
	connectedAt := d.connectedAt
	lastReadAt := d.lastReadAt
	lastWriteAt := d.lastWriteAt
	outgoingQueue := d.outgoingQueue
	incomingQueue := d.incomingQueue
	d.mu.Unlock()

	if reason == "" {
		reason = relayExitOutgoingClosed
	}
	if readReason == "" {
		readReason = "none"
	}
	outgoingDepth := 0
	if outgoingQueue != nil {
		outgoingDepth = len(outgoingQueue)
	}
	incomingDepth, incomingCapacity := 0, 0
	if incomingQueue != nil {
		incomingDepth, incomingCapacity = len(incomingQueue), cap(incomingQueue)
	}
	slog.Info("Noise relay transport closed",
		"noise_event", "transport_closed",
		"noise_side", "harness",
		"noise_reason", string(reason),
		"read_reason", readReason,
		"messages_dequeued", messagesDequeued,
		"messages_written", messagesWritten,
		"pings_written", pingsWritten,
		"pongs_received", pongsReceived,
		"connection_age_ms", elapsedMillis(connectedAt),
		"last_read_age_ms", elapsedMillis(lastReadAt),
		"last_write_age_ms", elapsedMillis(lastWriteAt),
		"write_pending", writePending,
		"write_kind", writeKind,
		"write_wait_ms", writeWait,
		"frames_read", framesRead,
		"records_written", recordsWritten,
		"outgoing_queue_depth", outgoingDepth,
		"incoming_queue_depth", incomingDepth,
		"incoming_queue_capacity", incomingCapacity,
	)
}

// logRelayApplicationBackpressure reports a blocked delivery into the executor
// application queue separately from transport reachability (Rust #58025).
func logRelayApplicationBackpressure(wait time.Duration, depth, capacity int) {
	waitMS := uint64(0)
	if wait > 0 {
		waitMS = uint64(wait / time.Millisecond)
	}
	slog.Warn("Noise relay application backpressure",
		"noise_event", "application_backpressure",
		"noise_side", "harness",
		"noise_reason", "application_backpressure",
		"delivery_wait_ms", waitMS,
		"incoming_queue_depth", depth,
		"incoming_queue_capacity", capacity,
	)
}

// logNoiseRequestReceipt marks that a JSON-RPC request reached the executor's
// relay stream. It runs immediately after decoding, before the write into the
// executor application, so a stalled executor stays distinguishable from a
// request that never arrived (Rust #58025). Only a conservative method name and
// the request's trace identifiers are exported.
func logNoiseRequestReceipt(message []byte) {
	var envelope struct {
		Method       string        `json:"method"`
		TraceContext *TraceContext `json:"traceContext"`
	}
	if err := json.Unmarshal(message, &envelope); err != nil || envelope.Method == "" {
		return
	}
	if !safeTraceMethod(envelope.Method) {
		return
	}
	attributes := []any{
		"noise_event", "request_received",
		"noise_side", "executor",
		"rpc.method", envelope.Method,
	}
	if trace := envelope.TraceContext; trace != nil {
		if _, err := uuid.Parse(strings.TrimSpace(trace.TraceID)); err == nil {
			attributes = append(attributes, "trace_id", trace.TraceID)
		}
		if _, err := uuid.Parse(strings.TrimSpace(trace.SpanID)); err == nil {
			attributes = append(attributes, "span_id", trace.SpanID)
		}
	}
	slog.Info("Noise relay request received", attributes...)
}

// safeTraceMethod bounds the peer-provided method name so it cannot smuggle
// arbitrary text into trace-safe telemetry.
func safeTraceMethod(method string) bool {
	if len(method) == 0 || len(method) > 64 {
		return false
	}
	for _, character := range method {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '/', character == '.', character == '_', character == '-':
		default:
			return false
		}
	}
	return true
}
