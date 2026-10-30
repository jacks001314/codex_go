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
// relay websocket. Writer and reader goroutines update it concurrently.
type relayTransportDiagnostics struct {
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
	d.exitReason = reason
}

func (d *relayTransportDiagnostics) recordMessageDequeued() {
	if d != nil {
		d.messagesDequeued++
	}
}

func (d *relayTransportDiagnostics) recordWriteStart(kind string) {
	if d != nil {
		d.writeStartedAt = time.Now()
		d.writeKind = kind
	}
}

func (d *relayTransportDiagnostics) recordWriteComplete(kind string, records int) {
	if d == nil {
		return
	}
	d.writeStartedAt = time.Time{}
	d.writeKind = ""
	d.lastWriteAt = time.Now()
	d.messagesWritten++
	d.recordsWritten += uint64(records)
	if kind == "keepalive" {
		d.pingsWritten++
		d.lastPingAt = time.Now()
		d.logHeartbeat("ping_written")
	}
}

func (d *relayTransportDiagnostics) recordPongReceived() {
	if d == nil || d.lastPingAt.IsZero() {
		// Unsolicited pongs must not create unbounded telemetry volume.
		return
	}
	d.pongsReceived++
	d.logHeartbeat("pong_received")
	d.lastPingAt = time.Time{}
}

func (d *relayTransportDiagnostics) logHeartbeat(phase string) {
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
	if d != nil {
		d.framesRead++
		d.lastReadAt = time.Now()
	}
}

func (d *relayTransportDiagnostics) recordReadError(err error) {
	if d != nil {
		d.readReason = relayReadExitReason(err)
	}
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
	reason := d.exitReason
	if reason == "" {
		reason = relayExitOutgoingClosed
	}
	readReason := d.readReason
	if readReason == "" {
		readReason = "none"
	}
	writeKind := "none"
	if !d.writeStartedAt.IsZero() {
		writeKind = d.writeKind
	}
	outgoingDepth := 0
	if d.outgoingQueue != nil {
		outgoingDepth = len(d.outgoingQueue)
	}
	incomingDepth, incomingCapacity := 0, 0
	if d.incomingQueue != nil {
		incomingDepth, incomingCapacity = len(d.incomingQueue), cap(d.incomingQueue)
	}
	slog.Info("Noise relay transport closed",
		"noise_event", "transport_closed",
		"noise_side", "harness",
		"noise_reason", string(reason),
		"read_reason", readReason,
		"messages_dequeued", d.messagesDequeued,
		"messages_written", d.messagesWritten,
		"pings_written", d.pingsWritten,
		"pongs_received", d.pongsReceived,
		"connection_age_ms", elapsedMillis(d.connectedAt),
		"last_read_age_ms", elapsedMillis(d.lastReadAt),
		"last_write_age_ms", elapsedMillis(d.lastWriteAt),
		"write_pending", !d.writeStartedAt.IsZero(),
		"write_kind", writeKind,
		"write_wait_ms", elapsedMillis(d.writeStartedAt),
		"frames_read", d.framesRead,
		"records_written", d.recordsWritten,
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
