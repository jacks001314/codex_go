package remotecontrol

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNormalizeRemoteControlURLAcceptsChatGPTHTTPSURLs(t *testing.T) {
	target, err := NormalizeRemoteControlURL("https://chatgpt.com/backend-api")
	if err != nil {
		t.Fatalf("NormalizeRemoteControlURL(chatgpt) error = %v", err)
	}
	want := &RemoteControlTarget{
		WebSocketURL:  "wss://chatgpt.com/backend-api/wham/remote/control/server",
		EnrollURL:     "https://chatgpt.com/backend-api/wham/remote/control/server/enroll",
		RefreshURL:    "https://chatgpt.com/backend-api/wham/remote/control/server/refresh",
		PairURL:       "https://chatgpt.com/backend-api/wham/remote/control/server/pair",
		PairStatusURL: "https://chatgpt.com/backend-api/wham/remote/control/server/pair/status",
	}
	if *target != *want {
		t.Fatalf("target = %+v, want %+v", target, want)
	}

	target, err = NormalizeRemoteControlURL("https://api.chatgpt-staging.com/backend-api")
	if err != nil {
		t.Fatalf("NormalizeRemoteControlURL(staging) error = %v", err)
	}
	if target.WebSocketURL != "wss://api.chatgpt-staging.com/backend-api/wham/remote/control/server" {
		t.Fatalf("staging websocket = %q", target.WebSocketURL)
	}
}

func TestNormalizeRemoteControlURLAcceptsLocalhostURLs(t *testing.T) {
	target, err := NormalizeRemoteControlURL("http://localhost:8080/backend-api")
	if err != nil {
		t.Fatalf("NormalizeRemoteControlURL(localhost) error = %v", err)
	}
	if target.WebSocketURL != "ws://localhost:8080/backend-api/wham/remote/control/server" ||
		target.EnrollURL != "http://localhost:8080/backend-api/wham/remote/control/server/enroll" {
		t.Fatalf("localhost target = %+v", target)
	}

	target, err = NormalizeRemoteControlURL("https://localhost:8443/backend-api")
	if err != nil {
		t.Fatalf("NormalizeRemoteControlURL(localhost https) error = %v", err)
	}
	if target.WebSocketURL != "wss://localhost:8443/backend-api/wham/remote/control/server" {
		t.Fatalf("localhost https websocket = %q", target.WebSocketURL)
	}
}

func TestNormalizeRemoteControlURLRejectsUnsupportedURLs(t *testing.T) {
	for _, raw := range []string{
		"http://chatgpt.com/backend-api",
		"http://example.com/backend-api",
		"https://example.com/backend-api",
		"https://chat.openai.com/backend-api",
		"https://chatgpt.com.evil.com/backend-api",
		"https://evilchatgpt.com/backend-api",
		"https://foo.localhost/backend-api",
	} {
		_, err := NormalizeRemoteControlURL(raw)
		if err == nil {
			t.Fatalf("NormalizeRemoteControlURL(%q) unexpectedly succeeded", raw)
		}
		if !errors.Is(err, ErrInvalidRequest) && err.Error() != "invalid remote control URL `"+raw+"`; expected HTTPS URL for chatgpt.com or chatgpt-staging.com, or HTTP/HTTPS URL for localhost" {
			t.Fatalf("NormalizeRemoteControlURL(%q) error = %v", raw, err)
		}
	}
}

func TestRemoteControlClientEnvelopeJSONShapes(t *testing.T) {
	streamID := StreamID("stream-1")
	seqID := uint64(7)
	message := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	envelope := ClientEnvelope{
		Type:     ClientEventClientMessage,
		Message:  message,
		ClientID: ClientID("client-1"),
		StreamID: &streamID,
		SeqID:    &seqID,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal ClientEnvelope error = %v", err)
	}
	want := `{"type":"client_message","message":{"jsonrpc":"2.0","id":1,"method":"initialize"},"client_id":"client-1","stream_id":"stream-1","seq_id":7}`
	if string(data) != want {
		t.Fatalf("client envelope JSON = %s, want %s", data, want)
	}

	segmentID := 0
	ack := ClientEnvelope{
		Type:      ClientEventAck,
		SegmentID: &segmentID,
		ClientID:  ClientID("client-1"),
		StreamID:  &streamID,
		SeqID:     &seqID,
	}
	data, err = json.Marshal(ack)
	if err != nil {
		t.Fatalf("Marshal ack error = %v", err)
	}
	want = `{"type":"ack","segment_id":0,"client_id":"client-1","stream_id":"stream-1","seq_id":7}`
	if string(data) != want {
		t.Fatalf("ack JSON = %s, want %s", data, want)
	}
}

func TestRemoteControlServerEnvelopeJSONShapes(t *testing.T) {
	status := PongStatusActive
	pong := ServerEnvelope{
		Type:     ServerEventPong,
		Status:   &status,
		ClientID: ClientID("client-1"),
		StreamID: StreamID("stream-1"),
		SeqID:    1,
	}
	data, err := json.Marshal(pong)
	if err != nil {
		t.Fatalf("Marshal pong error = %v", err)
	}
	want := `{"type":"pong","status":"active","client_id":"client-1","stream_id":"stream-1","seq_id":1}`
	if string(data) != want {
		t.Fatalf("pong JSON = %s, want %s", data, want)
	}

	segmentID := 0
	segmentCount := 2
	messageSize := 10
	chunk := "YWJj"
	envelope := ServerEnvelope{
		Type:               ServerEventServerMessageChunk,
		SegmentID:          &segmentID,
		SegmentCount:       &segmentCount,
		MessageSizeBytes:   &messageSize,
		MessageChunkBase64: &chunk,
		ClientID:           ClientID("client-1"),
		StreamID:           StreamID("stream-1"),
		SeqID:              2,
	}
	data, err = json.Marshal(envelope)
	if err != nil {
		t.Fatalf("Marshal chunk error = %v", err)
	}
	want = `{"type":"server_message_chunk","segment_id":0,"segment_count":2,"message_size_bytes":10,"message_chunk_base64":"YWJj","client_id":"client-1","stream_id":"stream-1","seq_id":2}`
	if string(data) != want {
		t.Fatalf("chunk JSON = %s, want %s", data, want)
	}
	if got := envelope.ChunkSegmentID(); got == nil || *got != 0 {
		t.Fatalf("ChunkSegmentID() = %v, want 0", got)
	}
}
