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
