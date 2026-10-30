package realtime

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestExistingCallTransportWireShape(t *testing.T) {
	transport := ExistingCallTransport("rtc_123")
	encoded, err := json.Marshal(transport)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"type":"existingCall","callId":"rtc_123"}` {
		t.Fatalf("wire = %s", encoded)
	}
	var decoded StartTransport
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != "existingCall" || decoded.CallID != "rtc_123" {
		t.Fatalf("decoded = %#v", decoded)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("validate = %v", err)
	}
	if err := ExistingCallTransport("  ").Validate(); !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("empty call id error = %v", err)
	}
	var missingCallID StartTransport
	if err := json.Unmarshal([]byte(`{"type":"existingCall"}`), &missingCallID); err == nil {
		t.Fatal("a transport without a call id was accepted")
	}
}

// Mirrors Rust #41923: the per-call sideband endpoint is in-process metadata,
// so it must not appear on the wire and a client cannot inject it.
func TestExistingCallSidebandEndpointIsNotWireSerializedLikeRust(t *testing.T) {
	transport := ExistingCallTransportWithSideband("rtc_123", "https://sideband.example/v1")
	encoded, err := json.Marshal(transport)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"type":"existingCall","callId":"rtc_123"}` {
		t.Fatalf("encoded = %s", encoded)
	}
	var decoded StartTransport
	if err := json.Unmarshal([]byte(`{"type":"existingCall","callId":"rtc_123","sidebandBaseUrl":"https://sideband.example/v1"}`), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SidebandBaseURL != "" {
		t.Fatalf("client-supplied sideband override was accepted: %#v", decoded)
	}
}

// Mirrors Rust #41923: the per-call endpoint wins over the configured sideband
// endpoint and keeps runtime auth headers without adding bearer authorization.
func TestExistingCallSidebandPrefersPerCallEndpointLikeRust(t *testing.T) {
	var configuredMu sync.Mutex
	configuredRequests := 0
	configuredServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		configuredMu.Lock()
		configuredRequests++
		configuredMu.Unlock()
		http.Error(writer, "unexpected configured endpoint use", http.StatusInternalServerError)
	}))
	defer configuredServer.Close()

	var perCallMu sync.Mutex
	var perCallRequests []string
	var perCallAuthorization []string
	var perCallRuntimeAuth []string
	perCallServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		perCallMu.Lock()
		perCallRequests = append(perCallRequests, request.URL.String())
		perCallAuthorization = append(perCallAuthorization, request.Header.Get("Authorization"))
		perCallRuntimeAuth = append(perCallRuntimeAuth, request.Header.Get("x-runtime-auth"))
		perCallMu.Unlock()
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-request.Context().Done()
	}))
	defer perCallServer.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{
		SidebandBaseURL: configuredServer.URL,
		CallHeaders:     http.Header{"X-Runtime-Auth": []string{"runtime"}},
	})
	manager.SetNotificationSink(func(Notification) {})
	includeStartupContext := false
	if _, _, err := manager.Start(&StartParams{
		ThreadID:              "thread-per-call-endpoint",
		OutputModality:        OutputAudio,
		Transport:             ExistingCallTransportWithSideband("rtc_per_call", perCallServer.URL),
		IncludeStartupContext: &includeStartupContext,
	}); err != nil {
		t.Fatalf("start existing call: %v", err)
	}
	defer func() { _, _, _ = manager.Stop(&StopParams{ThreadID: "thread-per-call-endpoint"}, "requested") }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		perCallMu.Lock()
		count := len(perCallRequests)
		perCallMu.Unlock()
		if count > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	configuredMu.Lock()
	configuredCount := configuredRequests
	configuredMu.Unlock()
	if configuredCount != 0 {
		t.Fatalf("configured endpoint received %d requests, want 0", configuredCount)
	}
	perCallMu.Lock()
	defer perCallMu.Unlock()
	if len(perCallRequests) == 0 {
		t.Fatal("the per-call sideband never connected")
	}
	if !strings.Contains(perCallRequests[0], "call_id=rtc_per_call") {
		t.Fatalf("sideband request = %q", perCallRequests[0])
	}
	if got := perCallAuthorization[0]; got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
	if got := perCallRuntimeAuth[0]; got != "runtime" {
		t.Fatalf("x-runtime-auth = %q, want runtime", got)
	}
}

// Mirrors Rust #41923: reconnecting an existing call reuses the per-call
// endpoint rather than the configured one.
func TestExistingCallSidebandReconnectsToPerCallEndpointLikeRust(t *testing.T) {
	var configuredMu sync.Mutex
	configuredRequests := 0
	configuredServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		configuredMu.Lock()
		configuredRequests++
		configuredMu.Unlock()
		http.Error(writer, "unexpected configured endpoint use", http.StatusInternalServerError)
	}))
	defer configuredServer.Close()

	var perCallMu sync.Mutex
	perCallRequests := 0
	perCallServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		perCallMu.Lock()
		perCallRequests++
		perCallMu.Unlock()
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-request.Context().Done()
	}))
	defer perCallServer.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{SidebandBaseURL: configuredServer.URL})
	manager.SetNotificationSink(func(Notification) {})
	version := VersionV3
	includeStartupContext := false
	const threadID = "thread-per-call-reconnect"
	if _, _, err := manager.Start(&StartParams{
		ThreadID:              threadID,
		OutputModality:        OutputAudio,
		Version:               &version,
		Transport:             ExistingCallTransportWithSideband("rtc_per_call", perCallServer.URL),
		IncludeStartupContext: &includeStartupContext,
	}); err != nil {
		t.Fatalf("start existing call: %v", err)
	}
	defer func() { _, _, _ = manager.Stop(&StopParams{ThreadID: threadID}, "requested") }()

	waitForCount := func(read func() int, want int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if read() >= want || time.Now().After(deadline) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForCount(func() int {
		perCallMu.Lock()
		defer perCallMu.Unlock()
		return perCallRequests
	}, 1)
	manager.mu.Lock()
	connection := manager.connections[threadID]
	manager.mu.Unlock()
	if connection == nil {
		t.Fatal("the sideband connection was not recorded")
	}
	connection.closeNow()
	waitForCount(func() int {
		perCallMu.Lock()
		defer perCallMu.Unlock()
		return perCallRequests
	}, 2)

	perCallMu.Lock()
	perCallCount := perCallRequests
	perCallMu.Unlock()
	configuredMu.Lock()
	configuredCount := configuredRequests
	configuredMu.Unlock()
	if perCallCount < 2 {
		t.Fatalf("per-call endpoint connections = %d, want at least 2", perCallCount)
	}
	if configuredCount != 0 {
		t.Fatalf("configured endpoint received %d reconnects, want 0", configuredCount)
	}
}

func TestExistingCallRejectsSessionConfiguration(t *testing.T) {
	falseValue := false
	trueValue := true
	model := "gpt-realtime-1.5"
	voice := VoiceCove
	versionV2 := VersionV2
	filler := true
	tests := []struct {
		name   string
		params StartParams
	}{
		{
			name: "v2 calls",
			params: StartParams{
				ThreadID:              "thread-existing",
				OutputModality:        OutputAudio,
				Transport:             ExistingCallTransport("rtc_1"),
				IncludeStartupContext: &falseValue,
				Version:               &versionV2,
			},
		},
		{
			name: "omitted startup context still includes it",
			params: StartParams{
				ThreadID:       "thread-existing",
				OutputModality: OutputAudio,
				Transport:      ExistingCallTransport("rtc_1"),
			},
		},
		{
			name: "explicit startup context",
			params: StartParams{
				ThreadID:              "thread-existing",
				OutputModality:        OutputAudio,
				Transport:             ExistingCallTransport("rtc_1"),
				IncludeStartupContext: &trueValue,
			},
		},
		{
			name: "model override",
			params: StartParams{
				ThreadID:              "thread-existing",
				OutputModality:        OutputAudio,
				Transport:             ExistingCallTransport("rtc_1"),
				IncludeStartupContext: &falseValue,
				Model:                 &model,
			},
		},
		{
			name: "voice override",
			params: StartParams{
				ThreadID:              "thread-existing",
				OutputModality:        OutputAudio,
				Transport:             ExistingCallTransport("rtc_1"),
				IncludeStartupContext: &falseValue,
				Voice:                 &voice,
			},
		},
		{
			name: "delegation filler",
			params: StartParams{
				ThreadID:              "thread-existing",
				OutputModality:        OutputAudio,
				Transport:             ExistingCallTransport("rtc_1"),
				IncludeStartupContext: &falseValue,
				DelegationAckFiller:   &filler,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.params.Normalized("model", VersionV1, VoiceCove); !errors.Is(err, ErrInvalidRealtimeRequest) {
				t.Fatalf("error = %v, want ErrInvalidRealtimeRequest", err)
			}
		})
	}
}

func TestExistingCallResolvesToV1AndIgnoresConfiguredVoice(t *testing.T) {
	falseValue := false
	params := StartParams{
		ThreadID:              "thread-existing",
		OutputModality:        OutputAudio,
		Transport:             ExistingCallTransport("rtc_1"),
		IncludeStartupContext: &falseValue,
	}
	config, err := params.Normalized("model", VersionV2, VoiceCove)
	if err != nil {
		t.Fatal(err)
	}
	if config.Version != VersionV1 {
		t.Fatalf("version = %q, want v1", config.Version)
	}
	if config.Transport.Type != "existingCall" || config.Transport.CallID != "rtc_1" {
		t.Fatalf("transport = %#v", config.Transport)
	}
	// An existing call keeps its own negotiated voice.
	voices := BuiltinVoices()
	if config.Voice != voices.DefaultForVersion(VersionV1) {
		t.Fatalf("voice = %q, want the version default", config.Voice)
	}
	if config.IncludeStartupContext {
		t.Fatal("existing calls must not include startup context")
	}
}

func TestStartParamsDelegationAckFillerRoundTrip(t *testing.T) {
	payload := `{"threadId":"t1","outputModality":"audio","delegationAckFiller":false}`
	var params StartParams
	if err := json.Unmarshal([]byte(payload), &params); err != nil {
		t.Fatal(err)
	}
	if params.DelegationAckFiller == nil || *params.DelegationAckFiller {
		t.Fatalf("delegationAckFiller = %#v", params.DelegationAckFiller)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"delegationAckFiller":false`) {
		t.Fatalf("encoded = %s", encoded)
	}
	config, err := params.Normalized("model", VersionV3, VoiceCove)
	if err != nil {
		t.Fatal(err)
	}
	if config.DelegationAckFiller == nil || *config.DelegationAckFiller {
		t.Fatalf("resolved filler = %#v", config.DelegationAckFiller)
	}
}

func TestExistingCallSidebandJoinsWithoutSessionUpdate(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	var payloads []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests = append(requests, request.URL.String())
		mu.Unlock()
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, payload, err := conn.Read(request.Context())
			if err != nil {
				return
			}
			mu.Lock()
			payloads = append(payloads, string(payload))
			mu.Unlock()
		}
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{SidebandBaseURL: server.URL})
	manager.SetNotificationSink(func(Notification) {})

	includeStartupContext := false
	_, _, err := manager.Start(&StartParams{
		ThreadID:              "thread-existing",
		OutputModality:        OutputAudio,
		Transport:             ExistingCallTransport("rtc_existing"),
		IncludeStartupContext: &includeStartupContext,
	})
	if err != nil {
		t.Fatalf("start existing call: %v", err)
	}
	defer func() { _, _, _ = manager.Stop(&StopParams{ThreadID: "thread-existing"}, "requested") }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		count := len(requests)
		mu.Unlock()
		if count > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) == 0 {
		t.Fatal("the sideband never connected")
	}
	if !strings.Contains(requests[0], "call_id=rtc_existing") {
		t.Fatalf("sideband request = %q", requests[0])
	}
	for _, payload := range payloads {
		if strings.Contains(payload, "session.update") {
			t.Fatalf("an existing call received a session update: %q", payload)
		}
	}
}

// newExistingCallActivationServer serves a websocket that rejects the first
// rejectCount handshakes with the given status before accepting, counting the
// total number of handshake attempts.
func newExistingCallActivationServer(t *testing.T, rejectCount int, status int) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		attempts++
		current := attempts
		mu.Unlock()
		if current <= rejectCount {
			http.Error(writer, http.StatusText(status), status)
			return
		}
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		<-request.Context().Done()
	}))
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return attempts
	}
	return server, count
}

// TestExistingCallSidebandRetriesActivationLikeRust mirrors Rust #51460: a
// client-created call whose sideband returns 404 while ICE activates is retried
// up to three times before the attachment succeeds.
func TestExistingCallSidebandRetriesActivationLikeRust(t *testing.T) {
	server, attempts := newExistingCallActivationServer(t, 3, http.StatusNotFound)
	defer server.Close()

	sideband := newRealtimeSideband("thread-activation", "rtc_activation", &TransportBackendConfig{SidebandBaseURL: server.URL}, &SessionConfig{Version: VersionV3})
	defer sideband.cancel()
	connection, err := dialExistingCallSideband(sideband.ctx, sideband)
	if err != nil {
		t.Fatalf("dial existing call sideband: %v", err)
	}
	defer connection.closeNow()
	if got := attempts(); got != 4 {
		t.Fatalf("handshake attempts = %d, want 4", got)
	}
}

// TestExistingCallSidebandGivesUpAfterActivationBudgetLikeRust verifies the
// bounded retry budget: four 404 responses exhaust the activation retries.
func TestExistingCallSidebandGivesUpAfterActivationBudgetLikeRust(t *testing.T) {
	server, attempts := newExistingCallActivationServer(t, 4, http.StatusNotFound)
	defer server.Close()

	sideband := newRealtimeSideband("thread-activation", "rtc_activation", &TransportBackendConfig{SidebandBaseURL: server.URL}, &SessionConfig{Version: VersionV3})
	defer sideband.cancel()
	if _, err := dialExistingCallSideband(sideband.ctx, sideband); err == nil {
		t.Fatal("dial existing call sideband should fail after the activation budget is exhausted")
	}
	if got := attempts(); got != 4 {
		t.Fatalf("handshake attempts = %d, want 4", got)
	}
}

// TestExistingCallSidebandDoesNotRetryGoneLikeRust verifies that a 410 handshake
// is terminal and never retried during activation.
func TestExistingCallSidebandDoesNotRetryGoneLikeRust(t *testing.T) {
	server, attempts := newExistingCallActivationServer(t, 1, http.StatusGone)
	defer server.Close()

	sideband := newRealtimeSideband("thread-activation", "rtc_activation", &TransportBackendConfig{SidebandBaseURL: server.URL}, &SessionConfig{Version: VersionV3})
	defer sideband.cancel()
	if _, err := dialExistingCallSideband(sideband.ctx, sideband); err == nil {
		t.Fatal("dial existing call sideband should fail on a 410 handshake")
	}
	if got := attempts(); got != 1 {
		t.Fatalf("handshake attempts = %d, want 1", got)
	}
}
