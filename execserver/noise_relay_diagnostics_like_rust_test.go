package execserver

// Rust parity: #55498 adds correlated diagnostics for Noise relay handshakes.
// The executor side reports rejected, unfinished handshakes with a classified
// reason and a normalized stream id, rate limited per relay connection; the
// harness side reports elapsed time plus connection identifiers and classifies
// WebSocket read failures from locally typed errors. Raw wire text, validator
// errors and reset reasons are never exported.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"codex_go/network"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type capturedLogRecord struct {
	message string
	level   slog.Level
	attrs   map[string]any
}

type capturedLogs struct {
	mu      sync.Mutex
	records []capturedLogRecord
}

func (c *capturedLogs) handle(record slog.Record) {
	attrs := map[string]any{}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, capturedLogRecord{message: record.Message, level: record.Level, attrs: attrs})
}

func installCapturedLogs(t *testing.T) *capturedLogs {
	t.Helper()
	logs := &capturedLogs{}
	previous := slog.Default()
	slog.SetDefault(slog.New(loggingHandlerFunc(logs.handle)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func (c *capturedLogs) snapshot() []capturedLogRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedLogRecord(nil), c.records...)
}

// find returns the attributes of the first record whose message contains the
// fragment and whose attributes satisfy match.
func (c *capturedLogs) find(messageFragment string, match func(map[string]any) bool) map[string]any {
	for _, record := range c.snapshot() {
		if !strings.Contains(record.message, messageFragment) {
			continue
		}
		if match == nil || match(record.attrs) {
			return record.attrs
		}
	}
	return nil
}

// waitForLog polls until a matching record exists so assertions never depend on
// goroutine scheduling.
func (c *capturedLogs) waitForLog(t *testing.T, messageFragment string, match func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if attrs := c.find(messageFragment, match); attrs != nil {
			return attrs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no %q diagnostic matched; records = %#v", messageFragment, c.snapshot())
	return nil
}

func (c *capturedLogs) allText() string {
	var builder strings.Builder
	for _, record := range c.snapshot() {
		builder.WriteString(record.message)
		for key, value := range record.attrs {
			fmt.Fprintf(&builder, " %s=%v", key, value)
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

func TestHandshakeDiagnosticsAreCappedPerWindowLikeRust(t *testing.T) {
	start := time.Now()
	windowStart, count := start, 0
	for slot := 0; slot < maxHandshakeDiagnosticsPerMinute; slot++ {
		if got := handshakeDiagnosticSlot(&windowStart, &count, start); got != slot {
			t.Fatalf("slot %d = %d, want %d", slot, got, slot)
		}
	}
	if got := handshakeDiagnosticSlot(&windowStart, &count, start); got != maxHandshakeDiagnosticsPerMinute {
		t.Fatalf("limit slot = %d, want %d", got, maxHandshakeDiagnosticsPerMinute)
	}
	if got := handshakeDiagnosticSlot(&windowStart, &count, start); got != maxHandshakeDiagnosticsPerMinute+1 {
		t.Fatalf("suppressed slot = %d, want %d", got, maxHandshakeDiagnosticsPerMinute+1)
	}
	next := start.Add(handshakeDiagnosticWindow)
	for slot := 0; slot < maxHandshakeDiagnosticsPerMinute; slot++ {
		if got := handshakeDiagnosticSlot(&windowStart, &count, next); got != slot {
			t.Fatalf("next window slot %d = %d, want %d", slot, got, slot)
		}
	}
	if got := handshakeDiagnosticSlot(&windowStart, &count, next); got != maxHandshakeDiagnosticsPerMinute {
		t.Fatalf("next window limit slot = %d, want %d", got, maxHandshakeDiagnosticsPerMinute)
	}
}

func TestHandshakeDiagnosticNormalizesStreamIDsLikeRust(t *testing.T) {
	logs := installCapturedLogs(t)
	streamID := strings.ToUpper(uuid.NewString())
	recordHandshakeDiagnostic("environment-1", "registration-1", streamID, "handshake_request", nil)
	recordHandshakeDiagnostic("environment-1", "registration-1", "malformed-secret", "reset_pending_validation", nil)

	rejected := logs.waitForLog(t, "Noise executor handshake diagnostic", func(attrs map[string]any) bool {
		return attrs["noise_reason"] == "handshake_request"
	})
	if rejected["noise_outcome"] != "rejected" || rejected["noise_side"] != "executor" {
		t.Fatalf("rejection attrs = %#v", rejected)
	}
	if rejected["stream_id"] != strings.ToLower(streamID) {
		t.Fatalf("stream_id = %v, want %v", rejected["stream_id"], strings.ToLower(streamID))
	}
	resetReceived := logs.waitForLog(t, "Noise executor handshake diagnostic", func(attrs map[string]any) bool {
		return attrs["noise_reason"] == "reset_pending_validation"
	})
	if resetReceived["noise_outcome"] != "reset_received" || resetReceived["stream_id"] != "invalid" {
		t.Fatalf("reset attrs = %#v", resetReceived)
	}
	if strings.Contains(logs.allText(), "malformed-secret") {
		t.Fatal("unvalidated stream id leaked into diagnostics")
	}
}

func TestHandshakeDiagnosticsRateLimitMarkerLikeRust(t *testing.T) {
	logs := installCapturedLogs(t)
	previousWindow := handshakeDiagnosticWindow
	handshakeDiagnosticWindow = time.Hour
	t.Cleanup(func() { handshakeDiagnosticWindow = previousWindow })

	diagnostics := newHandshakeDiagnostics("environment-1", "registration-1")
	for range maxHandshakeDiagnosticsPerMinute + 5 {
		diagnostics.record(uuid.NewString(), "handshake_request", nil)
	}
	records := logs.snapshot()
	var diagnosticsCount, rateLimitedCount int
	for _, record := range records {
		switch {
		case record.message == "Noise executor handshake diagnostic":
			diagnosticsCount++
		case record.message == "Noise executor handshake diagnostics rate limited":
			rateLimitedCount++
			if record.attrs["noise_outcome"] != "rate_limited" {
				t.Fatalf("rate limit attrs = %#v", record.attrs)
			}
		}
	}
	if diagnosticsCount != maxHandshakeDiagnosticsPerMinute {
		t.Fatalf("diagnostics = %d, want %d", diagnosticsCount, maxHandshakeDiagnosticsPerMinute)
	}
	if rateLimitedCount != 1 {
		t.Fatalf("rate limit markers = %d, want 1", rateLimitedCount)
	}

	// A fresh window resumes diagnostics.
	diagnostics.windowStart = time.Now().Add(-2 * handshakeDiagnosticWindow)
	diagnostics.record(uuid.NewString(), "handshake_request", nil)
	if got := len(logs.snapshot()); got != len(records)+1 {
		t.Fatalf("records after window reset = %d, want %d", got, len(records)+1)
	}
}

func TestClassifyHarnessKeyValidationErrorLikeRust(t *testing.T) {
	status := 503
	for _, testCase := range []struct {
		name       string
		err        error
		wantReason string
		wantStatus *int
	}{
		{name: "network_policy", err: &network.PolicyError{Err: errors.New("denied")}, wantReason: "network_policy"},
		{name: "registry_auth", err: errors.New("environment registry authentication error: rejected"), wantReason: "registry_auth"},
		{name: "registry_auth_resolution", err: errors.New("environment registry auth resolution failed: missing credential"), wantReason: "registry_auth"},
		{name: "registry_http", err: &remoteRegistryHTTPError{StatusCode: 503}, wantReason: "registry_http", wantStatus: &status},
		{name: "registry_request_timeout", err: fmt.Errorf("environment registry request failed: %w", context.DeadlineExceeded), wantReason: "registry_request_timeout"},
		{name: "registry_request", err: fmt.Errorf("environment registry request failed: %w", errors.New("connection refused")), wantReason: "registry_request"},
		{name: "registry_config", err: errors.New("environment registry config error: bad URL"), wantReason: "registry_config"},
		{name: "validation_protocol", err: errors.New("exec-server protocol error: invalid validation response"), wantReason: "validation_protocol"},
		{name: "validation_error", err: errors.New("environment registry rejected Noise relay harness key"), wantReason: "validation_error"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			reason, gotStatus := classifyHarnessKeyValidationError(testCase.err)
			if reason != testCase.wantReason {
				t.Fatalf("reason = %q, want %q", reason, testCase.wantReason)
			}
			switch {
			case testCase.wantStatus == nil && gotStatus != nil:
				t.Fatalf("status = %d, want none", *gotStatus)
			case testCase.wantStatus != nil && (gotStatus == nil || *gotStatus != *testCase.wantStatus):
				t.Fatalf("status = %v, want %d", gotStatus, *testCase.wantStatus)
			}
		})
	}
}

func TestWebsocketReadErrorReasonClassifiesTypedErrorsLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want string
	}{
		{name: "connection_reset", err: syscall.ECONNRESET, want: "websocket_read_connection_reset"},
		{name: "connection_aborted", err: syscall.ECONNABORTED, want: "websocket_read_connection_aborted"},
		{name: "unexpected_eof", err: io.ErrUnexpectedEOF, want: "websocket_read_unexpected_eof"},
		{name: "timed_out", err: context.DeadlineExceeded, want: "websocket_read_timed_out"},
		{name: "closed", err: websocket.CloseError{Code: websocket.StatusNormalClosure}, want: "websocket_read_closed"},
		{name: "tls", err: errors.New("tls: failed to verify certificate"), want: "websocket_read_tls"},
		{name: "protocol", err: errors.New("websocket: bad handshake"), want: "websocket_read_protocol"},
		{name: "io_other", err: errors.New("read failed"), want: "websocket_read_io_other"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := websocketReadErrorReason(testCase.err); got != testCase.want {
				t.Fatalf("websocketReadErrorReason(%v) = %q, want %q", testCase.err, got, testCase.want)
			}
		})
	}
}

func TestValidateRemoteHarnessKeyForwardsStreamIDHeaderLikeRust(t *testing.T) {
	var mu sync.Mutex
	var headers []string
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers = append(headers, r.Header.Get("x-codex-noise-stream-id"))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
	}))
	defer registry.Close()

	streamID := uuid.NewString()
	for _, candidate := range []string{strings.ToUpper(streamID), "stream-not-a-uuid", ""} {
		err := validateRemoteHarnessKey(context.Background(), RemoteEnvironmentConfig{
			BaseURL: registry.URL, EnvironmentID: "environment-1", HTTPClient: registry.Client(),
		}, &remoteRegistrationResponse{ExecutorRegistrationID: "registration-1"}, RemotePublicKey{Suite: noiseChannelSuite}, "authorization", candidate)
		if err != nil {
			t.Fatalf("validateRemoteHarnessKey(%q) error = %v", candidate, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(headers) != 3 {
		t.Fatalf("validation requests = %d, want 3", len(headers))
	}
	if headers[0] != strings.ToLower(streamID) {
		t.Fatalf("forwarded stream id = %q, want %q", headers[0], strings.ToLower(streamID))
	}
	if headers[1] != "" || headers[2] != "" {
		t.Fatalf("invalid stream ids forwarded: %#v", headers[1:])
	}
}

func TestNoiseRelayRecordsCorrelatedHandshakeDiagnosticsLikeRust(t *testing.T) {
	logs := installCapturedLogs(t)
	validationStarted := make(chan struct{}, 1)
	releaseValidation := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseValidation) }) })

	rig := newRemoteRelayTestRig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body remoteHarnessValidationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		switch body.HarnessKeyAuthorization {
		case "http-authorization-secret":
			http.Error(w, "registry-response-secret", http.StatusServiceUnavailable)
		case "block-authorization-secret":
			validationStarted <- struct{}{}
			<-releaseValidation
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		default:
			_ = json.NewEncoder(w).Encode(remoteHarnessValidationResponse{Valid: true})
		}
	}))

	// A malformed hybrid handshake reaches cryptography, and the peer-controlled
	// stream id is normalized (or dropped) before export.
	malformedStreamID := strings.ToUpper(uuid.NewString())
	malformedRequest := rig.handshakeRequest(t, malformedStreamID, []byte("handshake-authorization-secret"))
	malformedRequest[len(malformedRequest)-1] ^= 1
	writeRemoteRelayTestFrame(t, rig.conn, newRelayHandshakeFrame(malformedStreamID, malformedRequest))
	readRemoteRelayTestReset(t, rig.conn, malformedStreamID)
	malformedAttrs := logs.waitForLog(t, "Noise executor handshake diagnostic", func(attrs map[string]any) bool {
		return attrs["noise_reason"] == "handshake_request"
	})
	if malformedAttrs["stream_id"] != strings.ToLower(malformedStreamID) ||
		malformedAttrs["noise_outcome"] != "rejected" ||
		malformedAttrs["environment_id"] != remoteRelayTestEnvironmentID ||
		malformedAttrs["executor_registration_id"] != remoteRelayTestRegistrationID {
		t.Fatalf("malformed handshake attrs = %#v", malformedAttrs)
	}

	// A registry HTTP failure reports only the status, never the response text.
	httpStreamID := uuid.NewString()
	writeRemoteRelayTestFrame(t, rig.conn, newRelayHandshakeFrame(httpStreamID, rig.handshakeRequest(t, httpStreamID, []byte("http-authorization-secret"))))
	readRemoteRelayTestReset(t, rig.conn, httpStreamID)
	httpAttrs := logs.waitForLog(t, "Noise executor handshake diagnostic", func(attrs map[string]any) bool {
		return attrs["noise_reason"] == "registry_http"
	})
	if status, ok := httpAttrs["registry_status"].(int64); !ok || status != http.StatusServiceUnavailable {
		t.Fatalf("registry status = %#v", httpAttrs["registry_status"])
	}
	if httpAttrs["stream_id"] != httpStreamID {
		t.Fatalf("registry stream id = %v, want %v", httpAttrs["stream_id"], httpStreamID)
	}

	// A reset while validation is pending ends the handshake as reset_received,
	// and the unauthenticated reset reason is never reported.
	resetStreamID := uuid.NewString()
	writeRemoteRelayTestFrame(t, rig.conn, newRelayHandshakeFrame(resetStreamID, rig.handshakeRequest(t, resetStreamID, []byte("block-authorization-secret"))))
	select {
	case <-validationStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("registry never received the pending validation")
	}
	writeRemoteRelayTestFrame(t, rig.conn, relayMessageFrame{
		Version:     relayMessageFrameVersion,
		StreamID:    resetStreamID,
		Kind:        relayFrameReset,
		ResetReason: "untrusted-reset-secret",
	})
	resetAttrs := logs.waitForLog(t, "Noise executor handshake diagnostic", func(attrs map[string]any) bool {
		return attrs["noise_reason"] == "reset_pending_validation"
	})
	if resetAttrs["noise_outcome"] != "reset_received" || resetAttrs["stream_id"] != resetStreamID {
		t.Fatalf("reset attrs = %#v", resetAttrs)
	}
	releaseOnce.Do(func() { close(releaseValidation) })

	for _, secret := range []string{
		"handshake-authorization-secret",
		"http-authorization-secret",
		"block-authorization-secret",
		"registry-response-secret",
		"untrusted-reset-secret",
		"authorization",
	} {
		if strings.Contains(logs.allText(), secret) {
			t.Fatalf("diagnostics leaked %q: %s", secret, logs.allText())
		}
	}
}

func TestNoiseHarnessHandshakeFailureDiagnosticsCorrelateLikeRust(t *testing.T) {
	logs := installCapturedLogs(t)
	executorIdentity, err := generateRemoteNoiseIdentity()
	if err != nil {
		t.Fatalf("generate executor identity: %v", err)
	}
	defer executorIdentity.Destroy()

	handshakeFramesRead := make(chan struct{}, 1)
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for range 2 {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
		}
		handshakeFramesRead <- struct{}{}
		// Stall the responder until the initialization deadline closes the socket.
		_, _, _ = conn.Read(r.Context())
	}))
	defer relay.Close()

	provider := &initializeTimeoutNoiseProvider{bundle: &NoiseRendezvousConnectBundle{
		WebSocketURL:            "ws" + strings.TrimPrefix(relay.URL, "http"),
		EnvironmentID:           "environment-1",
		ExecutorRegistrationID:  "registration-1",
		ExecutorPublicKey:       executorIdentity.PublicKey(),
		HarnessKeyAuthorization: "harness-authorization-secret",
	}}
	_, err = DialNoiseRendezvousClient(context.Background(), provider, DialClientOptions{
		ClientName:        "harness-diagnostics-test",
		InitializeTimeout: 50 * time.Millisecond,
	})
	var timeoutErr *InitializeTimedOutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("DialNoiseRendezvousClient() error = %v, want InitializeTimedOutError", err)
	}
	select {
	case <-handshakeFramesRead:
	case <-time.After(5 * time.Second):
		t.Fatal("relay never received the handshake frames")
	}

	attrs := logs.waitForLog(t, "Noise harness handshake failed", func(attrs map[string]any) bool {
		return attrs["noise_reason"] == "timeout"
	})
	if attrs["noise_side"] != "harness" || attrs["noise_outcome"] != "failed" {
		t.Fatalf("failure attrs = %#v", attrs)
	}
	if attrs["environment_id"] != "environment-1" || attrs["executor_registration_id"] != "registration-1" {
		t.Fatalf("failure correlation attrs = %#v", attrs)
	}
	if _, err := uuid.Parse(fmt.Sprint(attrs["stream_id"])); err != nil {
		t.Fatalf("stream_id = %v, want a UUID", attrs["stream_id"])
	}
	if _, ok := attrs["elapsed_ms"].(int64); !ok {
		t.Fatalf("elapsed_ms = %#v", attrs["elapsed_ms"])
	}
	if strings.Contains(logs.allText(), "harness-authorization-secret") {
		t.Fatalf("harness diagnostics leaked the authorization: %s", logs.allText())
	}
}
