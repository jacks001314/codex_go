package execserver

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type capturedDiagnosticLog struct {
	mu      sync.Mutex
	records []capturedLogRecord
}

func (c *capturedDiagnosticLog) Enabled(context.Context, slog.Level) bool { return true }

func (c *capturedDiagnosticLog) Handle(_ context.Context, record slog.Record) error {
	attrs := map[string]any{}
	record.Attrs(func(attribute slog.Attr) bool {
		attrs[attribute.Key] = attribute.Value.Any()
		return true
	})
	c.mu.Lock()
	c.records = append(c.records, capturedLogRecord{message: record.Message, level: record.Level, attrs: attrs})
	c.mu.Unlock()
	return nil
}

func (c *capturedDiagnosticLog) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capturedDiagnosticLog) WithGroup(string) slog.Handler      { return c }

func captureDiagnosticLogs(t *testing.T) *capturedDiagnosticLog {
	t.Helper()
	capture := &capturedDiagnosticLog{}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return capture
}

func valueAsInt(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case uint64:
		return int64(typed)
	default:
		return -1
	}
}

func (c *capturedDiagnosticLog) find(message string) *capturedLogRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index := len(c.records) - 1; index >= 0; index-- {
		if c.records[index].message == message {
			record := c.records[index]
			return &record
		}
	}
	return nil
}

// Rust #58025: write timeouts are distinct from transport errors, and closure
// summaries report fixed categories plus local counters only.
func TestNoiseTransportClosureDiagnosticsLikeRust(t *testing.T) {
	capture := captureDiagnosticLogs(t)
	outgoing := make(chan []byte, 4)
	incoming := make(chan remoteRelayIncoming, 8)
	diagnostics := newRelayTransportDiagnostics(outgoing, incoming)
	diagnostics.recordMessageDequeued()
	diagnostics.recordWriteStart("data")
	diagnostics.recordWriteComplete("data", 3)
	diagnostics.recordFrameRead()
	outgoing <- []byte("queued")
	diagnostics.setExitReason(relayExitWebsocketWriteTimeout)
	diagnostics.logClosure()

	record := capture.find("Noise relay transport closed")
	if record == nil {
		t.Fatal("missing transport closure record")
	}
	if record.attrs["noise_reason"] != "websocket_write_timeout" {
		t.Fatalf("noise_reason = %#v", record.attrs["noise_reason"])
	}
	for _, key := range []string{"messages_dequeued", "messages_written", "records_written", "frames_read", "outgoing_queue_depth", "incoming_queue_capacity", "connection_age_ms", "write_pending"} {
		if _, ok := record.attrs[key]; !ok {
			t.Fatalf("closure record missing %q: %#v", key, record.attrs)
		}
	}
	if record.attrs["write_pending"] != false {
		t.Fatalf("write_pending = %#v after a completed write", record.attrs["write_pending"])
	}
	// Peer-provided text and raw errors never enter the summary.
	for key, value := range record.attrs {
		if text, ok := value.(string); ok && strings.Contains(text, "queued") {
			t.Fatalf("attribute %q leaked payload text %q", key, text)
		}
	}
}

func TestNoiseWriteExitReasonsLikeRust(t *testing.T) {
	if got := relayWriteExitReason(context.DeadlineExceeded); got != relayExitWebsocketWriteTimeout {
		t.Fatalf("timeout reason = %q", got)
	}
	if got := relayWriteExitReason(errors.New("broken pipe")); got != relayExitWebsocketWrite {
		t.Fatalf("transport reason = %q", got)
	}
}

func TestNoiseHeartbeatIgnoresUnsolicitedPongsLikeRust(t *testing.T) {
	capture := captureDiagnosticLogs(t)
	diagnostics := newRelayTransportDiagnostics(nil, nil)
	diagnostics.recordPongReceived()
	if record := capture.find("Noise relay heartbeat"); record != nil {
		t.Fatalf("unsolicited pong emitted %#v", record.attrs)
	}
	diagnostics.recordWriteComplete("keepalive", 0)
	diagnostics.recordPongReceived()
	if record := capture.find("Noise relay heartbeat"); record == nil || record.attrs["phase"] != "pong_received" {
		t.Fatalf("heartbeat record = %#v", record)
	}
	if diagnostics.pongsReceived != 1 || diagnostics.pingsWritten != 1 {
		t.Fatalf("heartbeat counters = %d/%d", diagnostics.pingsWritten, diagnostics.pongsReceived)
	}
}

func TestNoiseApplicationBackpressureDiagnosticLikeRust(t *testing.T) {
	capture := captureDiagnosticLogs(t)
	logRelayApplicationBackpressure(150*time.Millisecond, 8, 8)
	record := capture.find("Noise relay application backpressure")
	if record == nil {
		t.Fatal("missing backpressure record")
	}
	if valueAsInt(record.attrs["delivery_wait_ms"]) != 150 || valueAsInt(record.attrs["incoming_queue_depth"]) != 8 || valueAsInt(record.attrs["incoming_queue_capacity"]) != 8 {
		t.Fatalf("backpressure attrs = %#v", record.attrs)
	}
}

// The receipt marker is emitted before application delivery and carries only
// trace identifiers plus a conservative method name.
func TestNoiseRequestReceiptLikeRust(t *testing.T) {
	capture := captureDiagnosticLogs(t)
	logNoiseRequestReceipt([]byte(`{"jsonrpc":"2.0","id":7,"method":"exec/process/start","params":{"secret":"payload"},"traceContext":{"traceId":"3f6f8b0e-3d4c-4d9a-9d1f-0f1d2f3a4b5c","spanId":"9a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"}}`))
	record := capture.find("Noise relay request received")
	if record == nil {
		t.Fatal("missing receipt record")
	}
	if record.attrs["rpc.method"] != "exec/process/start" {
		t.Fatalf("rpc.method = %#v", record.attrs["rpc.method"])
	}
	if record.attrs["trace_id"] == nil || record.attrs["span_id"] == nil {
		t.Fatalf("missing trace identifiers: %#v", record.attrs)
	}
	for key, value := range record.attrs {
		if text, ok := value.(string); ok && strings.Contains(text, "payload") {
			t.Fatalf("attribute %q leaked payload text", key)
		}
	}

	// Unsafe method text is dropped entirely.
	capture.records = nil
	logNoiseRequestReceipt([]byte(`{"method":"evil\nmethod"}`))
	if record := capture.find("Noise relay request received"); record != nil {
		t.Fatalf("unsafe method emitted %#v", record.attrs)
	}
}
