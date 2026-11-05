package remotecontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func captureDebugLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// Mirrors Rust #59497: the transport reports item-page write timing only for
// thread/items/list responses and only while debug logging is enabled.
func TestItemPageWriteLoggerReportsFramesAndOutcomeLikeRust(t *testing.T) {
	workers := &RemoteControlWebsocketConnectionWorkers{}
	itemPage := &QueuedServerEnvelope{
		ItemPage: true,
		Envelope: ServerEnvelope{
			Type:     ServerEventServerMessage,
			StreamID: StreamID("stream-1"),
			Message:  json.RawMessage(`{"jsonrpc":"2.0","id":17,"result":{"data":[]}}`),
		},
	}
	prepared := preparedServerEnvelope{
		payloads:     []string{"frame-one", "frame-two"},
		seqID:        4,
		prepareStart: time.Now(),
	}

	buf := captureDebugLogs(t)
	logWrite := workers.itemPageWriteLogger(context.Background(), itemPage, prepared)
	if logWrite == nil {
		t.Fatal("item page write logger missing at debug level")
	}
	logWrite("success")
	output := buf.String()
	for _, want := range []string{
		`msg="remote control item page write finished"`,
		"request_id=17",
		"stream_id=stream-1",
		"seq_id=4",
		"frame_count=2",
		"wire_bytes=18",
		"outcome=success",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("debug log %q missing %q", output, want)
		}
	}

	other := &QueuedServerEnvelope{Envelope: itemPage.Envelope}
	if logger := workers.itemPageWriteLogger(context.Background(), other, prepared); logger != nil {
		t.Fatal("non-item-page envelope produced item page timing")
	}

	var quiet bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&quiet, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if logger := workers.itemPageWriteLogger(context.Background(), itemPage, prepared); logger != nil {
		t.Fatal("item page timing was collected without debug logging")
	}
	if quiet.Len() != 0 {
		t.Fatalf("info logging emitted debug records: %q", quiet.String())
	}
}

// Mirrors Rust #59497: the transport log correlates the write with the request
// id carried by the JSON-RPC response.
func TestServerEnvelopeRequestIDLikeRust(t *testing.T) {
	cases := map[string]string{
		`{"jsonrpc":"2.0","id":7,"result":{}}`:       "7",
		`{"jsonrpc":"2.0","id":"call-9","error":{}}`: "call-9",
		`{"jsonrpc":"2.0","method":"note"}`:          "",
		`not-json`:                                   "",
	}
	for message, want := range cases {
		envelope := &ServerEnvelope{Message: json.RawMessage(message)}
		if got := serverEnvelopeRequestID(envelope); got != want {
			t.Fatalf("serverEnvelopeRequestID(%s) = %q, want %q", message, got, want)
		}
	}
	if got := serverEnvelopeRequestID(nil); got != "" {
		t.Fatalf("serverEnvelopeRequestID(nil) = %q", got)
	}
}
