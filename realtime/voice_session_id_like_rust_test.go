package realtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestStartParamsVoiceSessionIDSerializesLikeRust covers Rust #53784: the
// optional client-generated voice session id is a nullable wire field that is
// serialized alongside (and independently from) realtimeSessionId.
func TestStartParamsVoiceSessionIDSerializesLikeRust(t *testing.T) {
	voice := "voice_789"
	session := "sess_456"
	encoded, err := json.Marshal(StartParams{
		ThreadID:          "thread-a",
		OutputModality:    OutputAudio,
		RealtimeSessionID: &session,
		VoiceSessionID:    &voice,
	})
	if err != nil {
		t.Fatalf("marshal start params: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("unmarshal start params: %v", err)
	}
	if fields["voiceSessionId"] != "voice_789" {
		t.Fatalf("voiceSessionId = %#v, want voice_789", fields["voiceSessionId"])
	}
	if fields["realtimeSessionId"] != "sess_456" {
		t.Fatalf("realtimeSessionId = %#v, want sess_456", fields["realtimeSessionId"])
	}

	// Absent on input and null on output, matching Rust's optional = nullable.
	nullEncoded, err := json.Marshal(StartParams{ThreadID: "thread-b", OutputModality: OutputAudio})
	if err != nil {
		t.Fatalf("marshal start params without voice id: %v", err)
	}
	var nullFields map[string]any
	if err := json.Unmarshal(nullEncoded, &nullFields); err != nil {
		t.Fatalf("unmarshal start params without voice id: %v", err)
	}
	value, present := nullFields["voiceSessionId"]
	if !present || value != nil {
		t.Fatalf("voiceSessionId = %#v present=%v, want null", value, present)
	}

	var params StartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-c","outputModality":"audio","voiceSessionId":"voice_c"}`), &params); err != nil {
		t.Fatalf("unmarshal start params with voice id: %v", err)
	}
	if params.VoiceSessionID == nil || *params.VoiceSessionID != "voice_c" {
		t.Fatalf("decoded voiceSessionId = %v", params.VoiceSessionID)
	}
	config, err := params.Normalized("rt-model", "", "")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if config.VoiceSessionID != "voice_c" {
		t.Fatalf("config voice session id = %q, want voice_c", config.VoiceSessionID)
	}
}

// TestRealtimeSessionHeadersKeepVoiceAndRealtimeIDsDistinct covers the Rust
// #53784 header contract: realtimeSessionId rides on x-session-id while the
// client voice session id rides on voice-session-id.
func TestRealtimeSessionHeadersKeepVoiceAndRealtimeIDsDistinct(t *testing.T) {
	headers := make(http.Header)
	applyRealtimeSessionHeaders(headers, &SessionConfig{
		RealtimeSessionID: "sess_456",
		VoiceSessionID:    "voice_789",
	})
	if got := headers.Get("x-session-id"); got != "sess_456" {
		t.Fatalf("x-session-id = %q, want sess_456", got)
	}
	if got := headers.Get("voice-session-id"); got != "voice_789" {
		t.Fatalf("voice-session-id = %q, want voice_789", got)
	}

	onlyRealtime := make(http.Header)
	applyRealtimeSessionHeaders(onlyRealtime, &SessionConfig{RealtimeSessionID: "sess_456"})
	if got := onlyRealtime.Get("voice-session-id"); got != "" {
		t.Fatalf("voice-session-id = %q, want omitted", got)
	}
}

// TestRealtimeWebRTCCallForwardsVoiceSessionID covers the WebRTC half of Rust
// #53784: the call creation request carries the same voice-session-id header.
func TestRealtimeWebRTCCallForwardsVoiceSessionID(t *testing.T) {
	received := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.Header().Set("Location", "/v1/realtime/calls/rtc_call-1")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("answer-sdp"))
	}))
	defer server.Close()

	backend := &TransportBackendConfig{
		WebRTCCallBaseURL: server.URL,
		CallHeaders:       http.Header{"Authorization": []string{"Bearer call-auth"}},
		HTTPClient:        server.Client(),
	}
	config := &SessionConfig{
		ThreadID:          "thread-a",
		RealtimeSessionID: "sess_456",
		VoiceSessionID:    "voice_789",
		Model:             "rt-model",
		OutputModality:    OutputAudio,
		Version:           VersionV1,
		Voice:             VoiceCove,
		Transport:         *WebRTCTransport("offer-sdp"),
	}
	call, err := createRealtimeWebRTCCall(t.Context(), backend, config)
	if err != nil {
		t.Fatalf("create realtime call: %v", err)
	}
	if call.CallID != "rtc_call-1" || call.SDP != "answer-sdp" {
		t.Fatalf("call = %#v", call)
	}
	headers := <-received
	if got := headers.Get("voice-session-id"); got != "voice_789" {
		t.Fatalf("voice-session-id = %q, want voice_789", got)
	}
	if got := headers.Get("x-session-id"); got != "sess_456" {
		t.Fatalf("x-session-id = %q, want sess_456", got)
	}
	if got := headers.Get("Authorization"); got != "Bearer call-auth" {
		t.Fatalf("Authorization = %q", got)
	}
}
