package realtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
)

func TestAudioChunkValidate(t *testing.T) {
	chunk := &AudioChunk{
		Data:        base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		SampleRate:  24000,
		NumChannels: 1,
	}
	if err := chunk.Validate(); err != nil {
		t.Fatalf("validate audio: %v", err)
	}
	chunk.Data = "not-base64"
	chunk.SampleRate = 0
	chunk.NumChannels = 0
	if err := chunk.Validate(); err != nil {
		t.Fatalf("Rust accepts typed audio values without local content validation: %v", err)
	}
}

func TestRealtimeAudioAndTransportJSONRequiredFieldsMatchRust(t *testing.T) {
	var audio AudioChunk
	if err := json.Unmarshal([]byte(`{"data":"","sampleRate":0,"numChannels":0,"samplesPerChannel":null,"itemId":null}`), &audio); err != nil {
		t.Fatalf("zero-valued audio fields should deserialize: %v", err)
	}
	for _, payload := range []string{
		`{"sampleRate":24000,"numChannels":1}`,
		`{"data":"AA==","numChannels":1}`,
		`{"data":"AA==","sampleRate":24000}`,
	} {
		if err := json.Unmarshal([]byte(payload), &audio); err == nil {
			t.Fatalf("missing required audio field was accepted: %s", payload)
		}
	}
	var params AppendAudioParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a"}`), &params); err == nil {
		t.Fatal("missing audio object was accepted")
	}

	var transport StartTransport
	if err := json.Unmarshal([]byte(`{"type":"webrtc","sdp":""}`), &transport); err != nil {
		t.Fatalf("empty but present SDP should deserialize: %v", err)
	}
	if encoded, err := json.Marshal(transport); err != nil || string(encoded) != `{"type":"webrtc","sdp":""}` {
		t.Fatalf("empty SDP transport JSON = %s, %v", encoded, err)
	}
	for _, payload := range []string{`{"type":"webrtc"}`, `{"sdp":"offer"}`, `{"type":"unknown"}`} {
		if err := json.Unmarshal([]byte(payload), &transport); err == nil {
			t.Fatalf("invalid transport was accepted: %s", payload)
		}
	}
}

func TestStartParamsNormalizeDefaultsAndPrompt(t *testing.T) {
	includeStartup := false
	var params StartParams
	if err := json.Unmarshal([]byte(`{
		"threadId":"thread-a",
		"outputModality":"audio",
		"prompt":null,
		"transport":{"type":"webrtc","sdp":"offer"}
	}`), &params); err != nil {
		t.Fatalf("unmarshal start params: %v", err)
	}
	params.IncludeStartupContext = &includeStartup
	config, err := params.Normalized("rt-model", "", "")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if config.Version != VersionV1 || config.Voice != VoiceCove || config.Model != "rt-model" || config.RealtimeSessionID != "thread-a" {
		t.Fatalf("defaults not applied: %+v", config)
	}
	if !config.PromptSet || config.Prompt != "" {
		t.Fatalf("prompt null semantics not preserved: %+v", config)
	}
	if config.IncludeStartupContext {
		t.Fatal("include startup context should be false")
	}
	if config.Transport.Type != "webrtc" || config.Transport.SDP != "offer" {
		t.Fatalf("transport = %+v", config.Transport)
	}
}

func TestStartParamsJSONMatchesRustOptionalAndRequiredFieldSemantics(t *testing.T) {
	encoded, err := json.Marshal(StartParams{ThreadID: "thr_123", OutputModality: OutputAudio})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"clientManagedHandoffs", "flushTranscriptTailOnSessionEnd", "codexResponsesAsItems",
		"codexResponseItemPrefix", "codexResponseHandoffMode", "codexResponseHandoffChannelPrefixes",
		"model", "includeStartupContext", "initialItems", "realtimeSessionId", "transport", "version", "voice",
	} {
		if field, ok := value[name]; !ok || field != nil {
			t.Fatalf("%s = %#v, want explicit null in %s", name, field, encoded)
		}
	}
	if _, ok := value["prompt"]; ok {
		t.Fatalf("unset prompt must be omitted: %s", encoded)
	}

	var params StartParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a","outputModality":"audio"}`), &params); err != nil {
		t.Fatal(err)
	}
	if params.Prompt.Set {
		t.Fatal("absent prompt was marked as set")
	}
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a"}`), &params); err == nil {
		t.Fatal("missing outputModality should fail during JSON decoding")
	}
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a","outputModality":"audio","version":"v4"}`), &params); err == nil {
		t.Fatal("unknown version should fail during JSON decoding")
	}
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a","outputModality":"audio","initialItems":[{"text":"missing role"}]}`), &params); err == nil {
		t.Fatal("missing initial item role should fail during JSON decoding")
	}
}

func TestAppendTextJSONDefaultsRoleAndRequiresRustFields(t *testing.T) {
	var params AppendTextParams
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a","text":"hello"}`), &params); err != nil {
		t.Fatal(err)
	}
	if params.Role != RoleUser {
		t.Fatalf("default role = %q, want user", params.Role)
	}
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a"}`), &params); err == nil {
		t.Fatal("missing text should fail during JSON decoding")
	}
	if err := json.Unmarshal([]byte(`{"threadId":"thread-a","text":"hello","role":"system"}`), &params); err == nil {
		t.Fatal("unknown role should fail during JSON decoding")
	}
}

func TestStopCancelsPendingWebRTCSidebandHandshake(t *testing.T) {
	const (
		threadID  = "thread-pending-sideband"
		offerSDP  = "v=0\r\no=offer\r\n"
		answerSDP = "v=0\r\no=answer\r\n"
		callID    = "rtc_pending"
	)

	callRequest := make(chan error, 1)
	callServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/realtime/calls" {
			callRequest <- fmt.Errorf("call path = %q", request.URL.Path)
			response.WriteHeader(http.StatusNotFound)
			return
		}
		if request.URL.Query().Get("intent") != "quicksilver" || request.URL.Query().Get("architecture") != "avas" {
			callRequest <- fmt.Errorf("call query = %q", request.URL.RawQuery)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := request.ParseMultipartForm(1 << 20); err != nil {
			callRequest <- fmt.Errorf("parse multipart call: %w", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := request.FormValue("sdp"); got != offerSDP {
			callRequest <- fmt.Errorf("offer SDP = %q", got)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		var session map[string]any
		if err := json.Unmarshal([]byte(request.FormValue("session")), &session); err != nil {
			callRequest <- fmt.Errorf("decode session: %w", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if session["type"] != "quicksilver" || session["instructions"] == nil {
			callRequest <- fmt.Errorf("session = %#v", session)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		callRequest <- nil
		response.Header().Set("Location", "/v1/realtime/calls/"+callID)
		response.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(response, answerSDP)
	}))
	defer callServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for sideband: %v", err)
	}
	defer listener.Close()
	pendingHandshake := make(chan struct{}, 1)
	releaseHandshake := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseHandshake) }) }
	defer release()
	type sidebandResult struct {
		request []byte
		err     error
	}
	sidebandDone := make(chan sidebandResult, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			sidebandDone <- sidebandResult{err: acceptErr}
			return
		}
		defer connection.Close()
		var request bytes.Buffer
		buffer := make([]byte, 1)
		for !bytes.Contains(request.Bytes(), []byte("\r\n\r\n")) {
			count, readErr := connection.Read(buffer)
			if count > 0 {
				request.Write(buffer[:count])
			}
			if readErr != nil {
				sidebandDone <- sidebandResult{request: request.Bytes(), err: fmt.Errorf("read sideband handshake: %w", readErr)}
				return
			}
		}
		pendingHandshake <- struct{}{}
		<-releaseHandshake
		_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
		rest, readErr := io.ReadAll(connection)
		request.Write(rest)
		if readErr != nil {
			sidebandDone <- sidebandResult{request: request.Bytes(), err: fmt.Errorf("wait for sideband close: %w", readErr)}
			return
		}
		sidebandDone <- sidebandResult{request: request.Bytes()}
	}()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{
		WebRTCCallBaseURL: callServer.URL,
		SidebandBaseURL:   "http://" + listener.Addr().String(),
		HTTPClient:        &http.Client{Timeout: 10 * time.Second},
		Headers:           http.Header{"Authorization": []string{"Bearer websocket-auth"}},
		CallHeaders:       http.Header{"Authorization": []string{"Bearer call-sideband-auth"}},
	})
	asyncNotifications := make(chan Notification, 1)
	manager.SetNotificationSink(func(notification Notification) { asyncNotifications <- notification })

	_, notifications, err := manager.Start(&StartParams{
		ThreadID:       threadID,
		OutputModality: OutputAudio,
		Transport:      WebRTCTransport(offerSDP),
	})
	if err != nil {
		t.Fatalf("start realtime: %v", err)
	}
	if err := <-callRequest; err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 2 || notifications[1].Method != NotificationSDP {
		t.Fatalf("start notifications = %#v", notifications)
	}
	sdp, ok := notifications[1].Params.(SDPNotification)
	if !ok || sdp.SDP != answerSDP {
		t.Fatalf("SDP notification = %#v", notifications[1].Params)
	}
	select {
	case <-pendingHandshake:
	case <-time.After(10 * time.Second):
		t.Fatal("sideband HTTP upgrade did not become pending")
	}

	_, closed, err := manager.Stop(&StopParams{ThreadID: threadID}, "requested")
	if err != nil {
		t.Fatalf("stop realtime: %v", err)
	}
	if closed.Method != NotificationClosed {
		t.Fatalf("closed notification = %#v", closed)
	}
	release()
	select {
	case result := <-sidebandDone:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !bytes.HasPrefix(result.request, []byte("GET /v1/realtime?call_id="+callID+" HTTP/1.1\r\n")) {
			t.Fatalf("sideband request = %q", result.request)
		}
		if !bytes.Contains(result.request, []byte("Authorization: Bearer call-sideband-auth\r\n")) || bytes.Contains(result.request, []byte("Authorization: Bearer websocket-auth\r\n")) {
			t.Fatalf("sideband auth headers = %q", result.request)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sideband connection remained open after stop")
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatalf("shutdown realtime: %v", err)
	}
	select {
	case notification := <-asyncNotifications:
		t.Fatalf("unexpected notification after cancellation: %#v", notification)
	default:
	}
}

func TestShutdownRejectsWebRTCStartWhoseCallCompletesLate(t *testing.T) {
	callPending := make(chan struct{}, 1)
	releaseCall := make(chan struct{})
	callServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		callPending <- struct{}{}
		<-releaseCall
		response.Header().Set("Location", "/v1/realtime/calls/rtc_late")
		response.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(response, "v=answer\r\n")
	}))
	defer callServer.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{
		WebRTCCallBaseURL: callServer.URL,
		SidebandBaseURL:   "ws://127.0.0.1:1",
	})
	startResult := make(chan error, 1)
	go func() {
		_, _, err := manager.Start(&StartParams{
			ThreadID:       "thread-late-start",
			OutputModality: OutputAudio,
			Transport:      WebRTCTransport("v=offer\r\n"),
		})
		startResult <- err
	}()
	select {
	case <-callPending:
	case <-time.After(10 * time.Second):
		t.Fatal("WebRTC call did not become pending")
	}

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	if err := manager.Shutdown(shutdownContext); err != nil {
		t.Fatalf("shutdown realtime: %v", err)
	}
	close(releaseCall)
	select {
	case err := <-startResult:
		if !errors.Is(err, ErrInvalidRealtimeRequest) || !strings.Contains(err.Error(), "shut down") {
			t.Fatalf("late start error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("late WebRTC start did not finish")
	}
	if _, ok := manager.State("thread-late-start"); ok {
		t.Fatal("late WebRTC start registered a session after shutdown")
	}
}

func TestRealtimeCallIDMatchesRustLocationParsing(t *testing.T) {
	for _, test := range []struct {
		name     string
		location string
		want     string
		wantErr  bool
	}{
		{name: "rtc", location: "/v1/realtime/calls/calls/rtc_backend_test?intent=quicksilver", want: "rtc_backend_test"},
		{name: "uuid", location: "/v1/live/019eb97d-8e9a-7ff3-94b0-ea019babd5d7", want: "019eb97d-8e9a-7ff3-94b0-ea019babd5d7"},
		{name: "invalid suffix", location: "/v1/realtime/calls/not-a-call-id", wantErr: true},
		{name: "route only", location: "/v1/realtime/calls", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := realtimeCallID(test.location)
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "does not contain a call id") {
					t.Fatalf("realtimeCallID(%q) = %q, %v", test.location, got, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("realtimeCallID(%q) = %q, %v; want %q", test.location, got, err, test.want)
			}
		})
	}
}

func TestV3WebRTCCallURLAndBodyMatchRustDirectAndBackendShapes(t *testing.T) {
	version := VersionV3
	config, err := (&StartParams{
		ThreadID:       "thread-v3-call",
		OutputModality: OutputAudio,
		Version:        &version,
		Transport:      WebRTCTransport("v=offer\r\n"),
	}).Normalized("", VersionV2, "")
	if err != nil {
		t.Fatalf("normalize v3 call: %v", err)
	}

	directBase := "https://api.openai.com/v1"
	directURL, err := realtimeCallURL(directBase, config)
	if err != nil || directURL != "https://api.openai.com/v1/live" {
		t.Fatalf("direct v3 call URL = %q, %v", directURL, err)
	}
	directBody, directContentType, err := realtimeCallBody(directBase, config)
	if err != nil {
		t.Fatalf("direct v3 call body: %v", err)
	}
	if directContentType != "multipart/form-data; boundary="+realtimeMultipartBoundary || !bytes.Contains(directBody, []byte(`"delegation":{"type":"client"}`)) {
		t.Fatalf("direct v3 body/content-type = %q %q", directContentType, directBody)
	}

	backendBase := "https://chatgpt.com/backend-api/codex"
	backendURL, err := realtimeCallURL(backendBase, config)
	if err != nil || backendURL != "https://chatgpt.com/backend-api/codex/realtime/calls?architecture=avas&intent=quicksilver" {
		t.Fatalf("backend v3 call URL = %q, %v", backendURL, err)
	}
	backendBody, backendContentType, err := realtimeCallBody(backendBase, config)
	if err != nil {
		t.Fatalf("backend v3 call body: %v", err)
	}
	if backendContentType != "application/json" {
		t.Fatalf("backend content type = %q", backendContentType)
	}
	var decoded map[string]any
	if err := json.Unmarshal(backendBody, &decoded); err != nil {
		t.Fatalf("decode backend body: %v", err)
	}
	sessionValue, _ := decoded["session"].(map[string]any)
	if decoded["sdp"] != "v=offer\r\n" || !nestedMapContains(sessionValue, "delegation", "type", "client") {
		t.Fatalf("backend v3 body = %#v", decoded)
	}
	if _, exists := sessionValue["id"]; exists {
		t.Fatalf("backend v3 session unexpectedly contains id: %#v", sessionValue)
	}
}

func TestV3StopSendsSessionCloseBeforeWebSocketClose(t *testing.T) {
	closeMessage := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		if err := conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"session.started","session":{"id":"session-v3-close"}}`)); err != nil {
			return
		}
		messageType, payload, err := conn.Read(request.Context())
		if err != nil || messageType != websocket.MessageText {
			return
		}
		var message map[string]any
		if json.Unmarshal(payload, &message) == nil {
			closeMessage <- message
		}
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	version := VersionV3
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-v3-close", OutputModality: OutputAudio, Version: &version}); err != nil {
		t.Fatalf("start v3 realtime: %v", err)
	}
	if _, _, err := manager.Stop(&StopParams{ThreadID: "thread-v3-close"}, "requested"); err != nil {
		t.Fatalf("stop v3 realtime: %v", err)
	}
	select {
	case message := <-closeMessage:
		if message["type"] != "session.close" {
			t.Fatalf("v3 close message = %#v", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for v3 session.close")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := manager.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown realtime manager: %v", err)
	}
}

func nestedMapContains(value map[string]any, parent string, key string, want any) bool {
	nested, ok := value[parent].(map[string]any)
	if !ok {
		return false
	}
	return nested[key] == want
}

func TestBinaryRealtimeFrameEmitsErrorThenClosedLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		_ = conn.Write(request.Context(), websocket.MessageBinary, []byte{1, 2, 3})
		<-request.Context().Done()
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	notifications := make(chan Notification, 4)
	manager.SetNotificationSink(func(notification Notification) { notifications <- notification })
	version := VersionV2
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-binary", OutputModality: OutputAudio, Version: &version}); err != nil {
		t.Fatalf("start V2 realtime: %v", err)
	}
	first := waitRealtimeNotification(t, notifications)
	second := waitRealtimeNotification(t, notifications)
	if first.Method != NotificationError || first.Params.(ErrorNotification).Message != "unexpected binary realtime websocket event" {
		t.Fatalf("first binary notification = %#v", first)
	}
	if second.Method != NotificationClosed || second.Params.(ClosedNotification).Reason == nil || *second.Params.(ClosedNotification).Reason != "error" {
		t.Fatalf("second binary notification = %#v", second)
	}
}

func TestNormalTransportCloseFlushesTranscriptTailBeforeClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		if err := conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"conversation.item.input_audio_transcription.completed","transcript":"remaining words"}`)); err != nil {
			return
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	order := make(chan string, 4)
	manager.SetEventSink(func(_ string, event Event) {
		if event.Type == "transcript_tail.flush" {
			if len(event.ActiveTranscript) != 1 || event.ActiveTranscript[0].Text != "remaining words" {
				t.Errorf("tail event = %#v", event)
			}
			order <- "tail"
		}
	})
	manager.SetNotificationSink(func(notification Notification) {
		if notification.Method == NotificationClosed {
			closed := notification.Params.(ClosedNotification)
			if closed.Reason == nil || *closed.Reason != "transport_closed" {
				t.Errorf("closed notification = %#v", notification)
			}
			order <- "closed"
		}
	})
	flushTail := true
	version := VersionV2
	if _, _, err := manager.Start(&StartParams{
		ThreadID:                 "thread-normal-close-tail",
		OutputModality:           OutputAudio,
		Version:                  &version,
		FlushTranscriptTailOnEnd: &flushTail,
	}); err != nil {
		t.Fatalf("start realtime: %v", err)
	}
	for _, want := range []string{"tail", "closed"} {
		select {
		case got := <-order:
			if got != want {
				t.Fatalf("close order got %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

func TestBEMChannelParserMatchesRustDefaultsOverridesAndFragmentation(t *testing.T) {
	defaults := newBEMChannelParser(nil)
	if text, ready := defaults.push("[COM"); ready || text != "" {
		t.Fatalf("partial default prefix = %q, %v", text, ready)
	}
	if text, ready := defaults.push("MENTARY]progress"); !ready || text != "[COMMENTARY]progress" || defaults.phase != "commentary" {
		t.Fatalf("completed default prefix = %q, %v, phase=%q", text, ready, defaults.phase)
	}
	if text, ready := defaults.push(" update"); !ready || text != " update" {
		t.Fatalf("post-prefix delta = %q, %v", text, ready)
	}

	overrides := map[string][]string{
		"analysis":   {"[THINKING]", "[THOUGHT]"},
		"commentary": {"[PROGRESS]", "[UPDATE]"},
	}
	for text, want := range map[string]string{
		"[THINKING]reasoning": "commentary",
		"[UPDATE]working":     "commentary",
		"[FINAL]finished":     "final_answer",
		"[ANALYSIS]old":       "",
	} {
		if got := bemMessagePhase(text, overrides); got != want {
			t.Fatalf("bemMessagePhase(%q) = %q, want %q", text, got, want)
		}
	}
	if got := bemMessagePhase("plain output", map[string][]string{"analysis": {""}}); got != "" {
		t.Fatalf("empty custom prefix matched plain output: %q", got)
	}

	unrecognized := newBEMChannelParser(nil)
	if text, ready := unrecognized.push("plain output"); ready || text != "" {
		t.Fatalf("unrecognized push = %q, %v", text, ready)
	}
	if text := unrecognized.finish(); text != "plain output" || unrecognized.phase != "final_answer" {
		t.Fatalf("unrecognized finish = %q, phase=%q", text, unrecognized.phase)
	}
}

func TestStartsForDifferentThreadsDoNotBlockEachOther(t *testing.T) {
	requests := make(chan struct{}, 2)
	gate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests <- struct{}{}
		<-gate
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(request.Context())
		<-request.Context().Done()
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})

	startErrors := make(chan error, 2)
	for _, threadID := range []string{"thread-concurrent-a", "thread-concurrent-b"} {
		threadID := threadID
		go func() {
			version := VersionV2
			_, _, err := manager.Start(&StartParams{ThreadID: threadID, OutputModality: OutputText, Version: &version})
			startErrors <- err
		}()
	}
	for index := 0; index < 2; index++ {
		select {
		case <-requests:
		case <-time.After(2 * time.Second):
			close(gate)
			t.Fatal("starts for different threads were serialized")
		}
	}
	close(gate)
	for index := 0; index < 2; index++ {
		select {
		case err := <-startErrors:
			if err != nil {
				t.Fatalf("concurrent realtime start: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for concurrent realtime start")
		}
	}
}

func waitRealtimeNotification(t *testing.T, notifications <-chan Notification) Notification {
	t.Helper()
	select {
	case notification := <-notifications:
		return notification
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for realtime notification")
		return Notification{}
	}
}

func TestManagerRealWebsocketV3IsBidirectionalAndOrdered(t *testing.T) {
	type handshake struct {
		path   string
		query  string
		header http.Header
		update map[string]any
	}
	handshakes := make(chan handshake, 1)
	releaseStarted := make(chan struct{})
	outbound := make(chan []map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, payload, err := conn.Read(request.Context())
		if err != nil {
			return
		}
		var update map[string]any
		if json.Unmarshal(payload, &update) != nil {
			return
		}
		handshakes <- handshake{path: request.URL.Path, query: request.URL.RawQuery, header: request.Header.Clone(), update: update}
		<-releaseStarted
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"session.started","session":{"id":"session-v3"}}`))
		messages := make([]map[string]any, 0, 3)
		for len(messages) < 3 {
			_, payload, err = conn.Read(request.Context())
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil {
				messages = append(messages, message)
			}
		}
		outbound <- messages
		for _, event := range []string{
			`{"type":"input_transcript.added","item":{"text":"hello"}}`,
			`{"type":"turn.done","turn":{"role":"assistant","transcript":"hi"}}`,
			`{"type":"output_audio.delta","audio":"AQI="}`,
			`{"type":"delegation.created","item":{"type":"delegation","target":"client","id":"handoff-1","content":[{"type":"input_text","text":"fix it"}]}}`,
		} {
			if err := conn.Write(request.Context(), websocket.MessageText, []byte(event)); err != nil {
				return
			}
		}
		_ = conn.Close(websocket.StatusNormalClosure, "done")
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL, Headers: http.Header{"Authorization": []string{"Bearer test"}}})
	notifications := make(chan Notification, 8)
	events := make(chan Event, 8)
	manager.SetNotificationSink(func(notification Notification) { notifications <- notification })
	manager.SetEventSink(func(_ string, event Event) { events <- event })
	version := VersionV3
	startResult := make(chan error, 1)
	go func() {
		_, started, err := manager.Start(&StartParams{
			ThreadID: "thread-v3", OutputModality: OutputAudio, Version: &version,
			InitialItems: []InitialTextItem{{Role: RoleDeveloper, Text: "Remember this."}},
		})
		if err == nil && (len(started) != 1 || started[0].Method != NotificationStarted) {
			err = fmt.Errorf("start notifications = %#v", started)
		}
		startResult <- err
	}()

	handshakeInfo := <-handshakes
	if handshakeInfo.path != "/v1/live" || !strings.Contains(handshakeInfo.query, "model=gpt-live-1-boulder-alpha") {
		t.Fatalf("realtime URL = %s?%s", handshakeInfo.path, handshakeInfo.query)
	}
	if handshakeInfo.header.Get("Authorization") != "Bearer test" || handshakeInfo.header.Get("Openai-Alpha") != "quicksilver=v2" || handshakeInfo.header.Get("X-Session-Id") != "thread-v3" {
		t.Fatalf("realtime headers = %#v", handshakeInfo.header)
	}
	if handshakeInfo.update["type"] != "session.update" {
		t.Fatalf("session update = %#v", handshakeInfo.update)
	}
	sessionUpdate, _ := handshakeInfo.update["session"].(map[string]any)
	initialItems, _ := sessionUpdate["initial_items"].([]any)
	if len(initialItems) != 1 {
		t.Fatalf("session initial_items = %#v", sessionUpdate["initial_items"])
	}
	select {
	case err := <-startResult:
		t.Fatalf("v3 start returned before session.started: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseStarted)
	if err := <-startResult; err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.Type != "session.updated" || event.RealtimeSessionID != "session-v3" {
			t.Fatalf("replayed session event = %+v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for replayed session event")
	}

	if _, err := manager.AppendText(&AppendTextParams{ThreadID: "thread-v3", Text: "typed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendAudio(&AppendAudioParams{ThreadID: "thread-v3", Audio: validAudio()}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AppendSpeech(&AppendSpeechParams{ThreadID: "thread-v3", Text: "spoken"}); err != nil {
		t.Fatal(err)
	}
	writes := <-outbound
	if writes[0]["type"] != "session.context.append" || writes[1]["type"] != "input_audio.append" || writes[2]["type"] != "session.context.append" || writes[2]["channel"] != "speakable" {
		t.Fatalf("outbound messages = %#v", writes)
	}

	wantMethods := []NotificationMethod{NotificationTranscriptDelta, NotificationTranscriptDone, NotificationOutputAudioDelta, NotificationItemAdded, NotificationClosed}
	for _, want := range wantMethods {
		select {
		case notification := <-notifications:
			if notification.Method != want {
				t.Fatalf("notification method = %s, want %s", notification.Method, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s", want)
		}
	}
	foundHandoff := false
	for len(events) > 0 {
		event := <-events
		if event.Type == "handoff.requested" && event.HandoffID == "handoff-1" && event.InputTranscript == "fix it" {
			foundHandoff = true
		}
	}
	if !foundHandoff {
		t.Fatal("handoff event was not routed to event sink")
	}
}

func TestV3StartRejectsFirstParsedEventBeforeSessionStartedLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		_ = conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"input_transcript.added","item":{"text":"too early"}}`))
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	version := VersionV3
	_, _, err := manager.Start(&StartParams{ThreadID: "thread-v3-early", OutputModality: OutputAudio, Version: &version})
	if err == nil || !strings.Contains(err.Error(), "received an event before session.started") {
		t.Fatalf("start error = %v", err)
	}
	if _, ok := manager.State("thread-v3-early"); ok {
		t.Fatal("failed V3 handshake must not register a realtime session")
	}
}

func TestStartParamsValidateV3InitialItemsAndHandoffMode(t *testing.T) {
	mode := HandoffModeBemTags
	params := StartParams{ThreadID: "thread-v3", OutputModality: OutputAudio, Version: func() *Version { value := VersionV3; return &value }(), CodexResponseHandoffMode: &mode, InitialItems: []InitialTextItem{{Role: RoleDeveloper, Text: "Remember this."}, {Role: RoleAssistant, Text: "Understood."}}}
	config, err := params.Normalized("model", VersionV2, VoiceCove)
	if err != nil {
		t.Fatalf("normalize v3 params: %v", err)
	}
	if config.CodexResponseHandoffMode != HandoffModeBemTags || len(config.InitialItems) != 2 || config.InitialItems[0].Role != RoleDeveloper {
		t.Fatalf("config = %#v", config)
	}
	manager := NewManager()
	state, notifications, err := manager.Start(&params)
	if err != nil || state == nil || len(notifications) != 1 || notifications[0].Method != NotificationStarted {
		t.Fatalf("start state=%#v notifications=%#v err=%v", state, notifications, err)
	}
	tooMany := params
	tooMany.InitialItems = make([]InitialTextItem, 129)
	if err := tooMany.Validate(); !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("expected initial item limit error, got %v", err)
	}
	v2 := params
	v2.Version = func() *Version { value := VersionV2; return &value }()
	if _, err := v2.Normalized("model", VersionV2, VoiceMarin); !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("expected v2 initial item error, got %v", err)
	}
}

func TestV3BatchesCodexOutputRoutesBEMAndFlushesTranscriptTail(t *testing.T) {
	const threadID = "thread-v3-output"
	outbound := make(chan map[string]any, 8)
	serverErrors := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			serverErrors <- err
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			serverErrors <- fmt.Errorf("read session update: %w", err)
			return
		}
		for _, payload := range []string{
			`{"type":"session.started","session":{"id":"sess-v3"}}`,
			`{"type":"input_transcript.added","item":{"text":"delegate this"}}`,
			`{"type":"turn.done","turn":{"role":"user","transcript":"delegate this"}}`,
			`{"type":"delegation.created","item":{"type":"delegation","target":"client","id":"delegation-v3","content":[{"type":"input_text","text":"delegate this"}]}}`,
		} {
			if err := conn.Write(request.Context(), websocket.MessageText, []byte(payload)); err != nil {
				serverErrors <- fmt.Errorf("write setup event: %w", err)
				return
			}
		}
		for {
			_, payload, err := conn.Read(request.Context())
			if err != nil {
				return
			}
			var message map[string]any
			if err := json.Unmarshal(payload, &message); err != nil {
				serverErrors <- fmt.Errorf("decode outbound message: %w", err)
				return
			}
			outbound <- message
			if message["type"] == "delegation.context.append" {
				if err := conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"input_transcript.added","item":{"text":"remaining tail"}}`)); err != nil {
					return
				}
				if err := conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"turn.done","turn":{"role":"user","transcript":"remaining tail"}}`)); err != nil {
					return
				}
			}
		}
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	events := make(chan Event, 16)
	manager.SetEventSink(func(_ string, event Event) { events <- event })
	version := VersionV3
	mode := HandoffModeBemTags
	flushTail := true
	if _, _, err := manager.Start(&StartParams{
		ThreadID:                 threadID,
		OutputModality:           OutputAudio,
		Version:                  &version,
		CodexResponseHandoffMode: &mode,
		FlushTranscriptTailOnEnd: &flushTail,
	}); err != nil {
		t.Fatalf("start V3 realtime: %v", err)
	}
	waitRealtimeEvent(t, events, func(event Event) bool {
		return event.Type == "handoff.requested" && event.HandoffID == "delegation-v3"
	})

	manager.BeginCodexOutput(threadID, "item-commentary", "final_answer")
	manager.StreamCodexOutput(threadID, "item-commentary", "[COM")
	manager.StreamCodexOutput(threadID, "item-commentary", "MENTARY]working")
	select {
	case message := <-outbound:
		t.Fatalf("V3 stream flushed before 200ms batch interval: %#v", message)
	case <-time.After(100 * time.Millisecond):
	}
	commentary := waitRealtimeOutbound(t, outbound)
	assertV3ContextAppend(t, commentary, "delegation-v3", "[COMMENTARY]working", "commentary")
	manager.CompleteCodexOutput(threadID, "item-commentary", "final_answer", "[COMMENTARY]working")

	manager.BeginCodexOutput(threadID, "item-final", "commentary")
	manager.CompleteCodexOutput(threadID, "item-final", "commentary", "[FINAL]done")
	finalMessage := waitRealtimeOutbound(t, outbound)
	assertV3ContextAppend(t, finalMessage, "delegation-v3", "[FINAL]done", "speakable")

	waitRealtimeEvent(t, events, func(event Event) bool {
		return event.Type == "input_transcript.done" && event.Text == "remaining tail"
	})
	if _, _, err := manager.Stop(&StopParams{ThreadID: threadID}, "requested"); err != nil {
		t.Fatalf("stop V3 realtime: %v", err)
	}
	tail := waitRealtimeEvent(t, events, func(event Event) bool { return event.Type == "transcript_tail.flush" })
	if len(tail.ActiveTranscript) != 1 || tail.ActiveTranscript[0].Role != "user" || tail.ActiveTranscript[0].Text != "remaining tail" {
		t.Fatalf("transcript tail = %#v", tail.ActiveTranscript)
	}
	select {
	case err := <-serverErrors:
		t.Fatalf("V3 test server: %v", err)
	default:
	}
}

func TestV2QueuesResponseCreateAndAcknowledgesSteerCompletionAndNoop(t *testing.T) {
	const threadID = "thread-v2-handoff"
	serverEvents := make(chan string, 16)
	serverReady := make(chan struct{}, 1)
	outbound := make(chan map[string]any, 16)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		serverReady <- struct{}{}
		go func() {
			for {
				select {
				case payload := <-serverEvents:
					if err := conn.Write(request.Context(), websocket.MessageText, []byte(payload)); err != nil {
						return
					}
				case <-request.Context().Done():
					return
				}
			}
		}()
		for {
			_, payload, err := conn.Read(request.Context())
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil {
				outbound <- message
			}
		}
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	events := make(chan Event, 16)
	manager.SetEventSink(func(_ string, event Event) { events <- event })
	version := VersionV2
	if _, _, err := manager.Start(&StartParams{ThreadID: threadID, OutputModality: OutputAudio, Version: &version}); err != nil {
		t.Fatalf("start V2 realtime: %v", err)
	}
	select {
	case <-serverReady:
	case <-time.After(5 * time.Second):
		t.Fatal("V2 server did not receive session update")
	}

	serverEvents <- `{"type":"session.updated"}`
	serverEvents <- `{"type":"response.created","response":{"id":"response-1"}}`
	serverEvents <- `{"type":"conversation.item.done","item":{"type":"function_call","name":"background_agent","id":"item-1","call_id":"call-1","arguments":"{\"input\":\"first task\"}"}}`
	first := waitRealtimeEvent(t, events, func(event Event) bool { return event.Type == "handoff.requested" })
	if first.HandoffID != "call-1" || first.InputTranscript != "first task" {
		t.Fatalf("first V2 handoff = %#v", first)
	}

	serverEvents <- `{"type":"conversation.item.done","item":{"type":"function_call","name":"background_agent","id":"item-2","call_id":"call-2","arguments":"{\"input\":\"steer task\"}"}}`
	second := waitRealtimeEvent(t, events, func(event Event) bool { return event.Type == "handoff.requested" && event.HandoffID == "call-2" })
	if second.InputTranscript != "steer task" {
		t.Fatalf("second V2 handoff = %#v", second)
	}
	assertV2FunctionOutput(t, waitRealtimeOutbound(t, outbound), "call-2", "This was sent to steer the previous background agent task.")
	select {
	case message := <-outbound:
		t.Fatalf("response.create was not deferred while response active: %#v", message)
	case <-time.After(100 * time.Millisecond):
	}
	serverEvents <- `{"type":"response.done","response":{"id":"response-1"}}`
	deferred := waitRealtimeOutbound(t, outbound)
	if deferred["type"] != "response.create" {
		t.Fatalf("deferred response create = %#v", deferred)
	}

	serverEvents <- `{"type":"response.done","response":{"id":"response-2"}}`
	waitRealtimeEvent(t, events, func(event Event) bool { return event.Type == "response.done" && event.ResponseID == "response-2" })
	manager.CompleteCodexOutput(threadID, "agent-output", "final_answer", "finished work")
	assertV2UserText(t, waitRealtimeOutbound(t, outbound), "[BACKEND] finished work")
	manager.CompleteHandoff(threadID)
	assertV2FunctionOutput(t, waitRealtimeOutbound(t, outbound), "call-1", "Background agent finished. Use the preceding [BACKEND] messages as the result.")
	if created := waitRealtimeOutbound(t, outbound); created["type"] != "response.create" {
		t.Fatalf("completion response create = %#v", created)
	}

	serverEvents <- `{"type":"response.done","response":{"id":"response-3"}}`
	waitRealtimeEvent(t, events, func(event Event) bool { return event.Type == "response.done" && event.ResponseID == "response-3" })
	serverEvents <- `{"type":"conversation.item.done","item":{"type":"function_call","name":"remain_silent","id":"item-noop","call_id":"call-noop","arguments":"{}"}}`
	assertV2FunctionOutput(t, waitRealtimeOutbound(t, outbound), "call-noop", "")
	if _, _, err := manager.Stop(&StopParams{ThreadID: threadID}, "requested"); err != nil {
		t.Fatalf("stop V2 realtime: %v", err)
	}
}

func TestV2SpeechStartedTruncatesPlayedOutputAudioLikeRust(t *testing.T) {
	outbound := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		for _, payload := range []string{
			`{"type":"response.output_audio.delta","item_id":"audio-item","delta":"AQID","sample_rate":24000,"channels":1,"samples_per_channel":480}`,
			`{"type":"input_audio_buffer.speech_started","item_id":"audio-item"}`,
		} {
			if err := conn.Write(request.Context(), websocket.MessageText, []byte(payload)); err != nil {
				return
			}
		}
		for {
			_, payload, err := conn.Read(request.Context())
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil {
				outbound <- message
			}
		}
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	version := VersionV2
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-audio-truncate", OutputModality: OutputAudio, Version: &version}); err != nil {
		t.Fatalf("start v2 realtime: %v", err)
	}

	select {
	case message := <-outbound:
		if message["type"] != "conversation.item.truncate" || message["item_id"] != "audio-item" || message["content_index"] != float64(0) || message["audio_end_ms"] != float64(20) {
			t.Fatalf("audio truncate = %#v", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for output audio truncate")
	}
}

func TestV2PrefixesOnlyClientUserTextAndBackendSpeechLikeRust(t *testing.T) {
	outbound := make(chan map[string]any, 16)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		for {
			_, payload, err := conn.Read(request.Context())
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil {
				outbound <- message
			}
		}
	}))
	defer server.Close()

	manager := NewManager()
	manager.SetTransportBackend(&TransportBackendConfig{WebsocketBaseURL: server.URL})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = manager.Shutdown(ctx)
	})
	version := VersionV2
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-v2-prefixes", OutputModality: OutputAudio, Version: &version}); err != nil {
		t.Fatalf("start v2 realtime: %v", err)
	}

	for _, input := range []struct {
		text string
		want string
	}{
		{text: "hello", want: "[USER] hello"},
		{text: "[USER] already", want: "[USER] already"},
		{text: "", want: ""},
	} {
		if _, err := manager.AppendText(&AppendTextParams{ThreadID: "thread-v2-prefixes", Text: input.text}); err != nil {
			t.Fatalf("append text %q: %v", input.text, err)
		}
		assertV2UserText(t, waitRealtimeOutbound(t, outbound), input.want)
	}

	state, err := manager.AppendSpeech(&AppendSpeechParams{ThreadID: "thread-v2-prefixes", Text: " \t "})
	if err != nil || state.SpeechInputs != 0 {
		t.Fatalf("blank speech state=%#v err=%v", state, err)
	}
	select {
	case message := <-outbound:
		t.Fatalf("blank speech emitted message: %#v", message)
	case <-time.After(100 * time.Millisecond):
	}

	if _, err := manager.AppendSpeech(&AppendSpeechParams{ThreadID: "thread-v2-prefixes", Text: "manual update"}); err != nil {
		t.Fatalf("append speech: %v", err)
	}
	assertV2UserText(t, waitRealtimeOutbound(t, outbound), "[BACKEND] manual update")
	if created := waitRealtimeOutbound(t, outbound); created["type"] != "response.create" {
		t.Fatalf("speech response create = %#v", created)
	}
}

func TestRealtimeOutputTruncationMatchesRustTokenBudgetAndUTF8Boundaries(t *testing.T) {
	short := "short output"
	if got := truncateRealtimeOutput(short); got != short {
		t.Fatalf("short output = %q", got)
	}
	if got := truncateRealtimeMiddleTokens("abcdef", 0); got != "\u20262 tokens truncated\u2026" {
		t.Fatalf("zero-budget output = %q", got)
	}

	input := "START-" + strings.Repeat("\u4e2d", 2_000) + "-END"
	got := truncateRealtimeOutput(input)
	if !utf8.ValidString(got) {
		t.Fatal("truncated output is not valid UTF-8")
	}
	if !strings.HasPrefix(got, "START-") || !strings.HasSuffix(got, "-END") {
		t.Fatalf("truncated output did not retain both ends: %q", got)
	}
	if !strings.Contains(got, "tokens truncated") {
		t.Fatalf("truncated output has no marker: %q", got)
	}
	if tokens := (len(got) + 3) / 4; tokens > 1_000 {
		t.Fatalf("truncated output uses %d approximate tokens", tokens)
	}
}

func waitRealtimeEvent(t *testing.T, events <-chan Event, match func(Event) bool) Event {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event := <-events:
			if match(event) {
				return event
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for realtime event")
			return Event{}
		}
	}
}

func waitRealtimeOutbound(t *testing.T, outbound <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case message := <-outbound:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for realtime outbound message")
		return nil
	}
}

func assertV3ContextAppend(t *testing.T, message map[string]any, handoffID, text, channel string) {
	t.Helper()
	if message["type"] != "delegation.context.append" || message["delegation_item_id"] != handoffID || message["channel"] != channel {
		t.Fatalf("V3 context append envelope = %#v", message)
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("V3 context append content = %#v", message["content"])
	}
	item, ok := content[0].(map[string]any)
	if !ok || item["type"] != "input_text" || item["text"] != text {
		t.Fatalf("V3 context append item = %#v", content[0])
	}
}

func assertV2FunctionOutput(t *testing.T, message map[string]any, callID, output string) {
	t.Helper()
	item, ok := message["item"].(map[string]any)
	if message["type"] != "conversation.item.create" || !ok || item["type"] != "function_call_output" || item["call_id"] != callID || item["output"] != output {
		t.Fatalf("V2 function output = %#v", message)
	}
}

func assertV2UserText(t *testing.T, message map[string]any, text string) {
	t.Helper()
	item, ok := message["item"].(map[string]any)
	if message["type"] != "conversation.item.create" || !ok || item["type"] != "message" || item["role"] != "user" {
		t.Fatalf("V2 user text envelope = %#v", message)
	}
	content, ok := item["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("V2 user text content = %#v", item["content"])
	}
	part, ok := content[0].(map[string]any)
	if !ok || part["type"] != "input_text" || part["text"] != text {
		t.Fatalf("V2 user text part = %#v", content[0])
	}
}

func TestBuiltinVoices(t *testing.T) {
	voices := BuiltinVoices()
	if voices.DefaultForVersion(VersionV1) != VoiceCove {
		t.Fatalf("default v1 = %s", voices.DefaultForVersion(VersionV1))
	}
	if voices.DefaultForVersion(VersionV2) != VoiceMarin {
		t.Fatalf("default v2 = %s", voices.DefaultForVersion(VersionV2))
	}
	if voices.DefaultForVersion(VersionV3) != VoiceCove {
		t.Fatalf("default v3 = %s", voices.DefaultForVersion(VersionV3))
	}
	if len(voices.V1) == 0 || len(voices.V2) == 0 {
		t.Fatalf("voices should not be empty: %+v", voices)
	}
}

func TestManagerLifecycle(t *testing.T) {
	manager := NewManager()
	now := fixedTime()
	manager.SetClock(func() time.Time { return now })
	state, notifications, err := manager.Start(&StartParams{
		ThreadID:       "thread-a",
		OutputModality: OutputAudio,
		Transport:      WebRTCTransport("offer"),
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if state.StartedAt != now || state.Config.Version != VersionV1 {
		t.Fatalf("state = %+v", state)
	}
	if len(notifications) != 2 || notifications[0].Method != NotificationStarted || notifications[1].Method != NotificationSDP {
		t.Fatalf("notifications = %+v", notifications)
	}
	restarted, restartNotifications, err := manager.Start(&StartParams{ThreadID: "thread-a", OutputModality: OutputText})
	if err != nil || restarted == nil || restarted.TextInputs != 0 || len(restartNotifications) != 1 || restartNotifications[0].Method != NotificationStarted {
		t.Fatalf("second start did not replace session: state=%#v notifications=%#v err=%v", restarted, restartNotifications, err)
	}

	now = now.Add(time.Second)
	audio := validAudio()
	state, err = manager.AppendAudio(&AppendAudioParams{ThreadID: "thread-a", Audio: audio})
	if err != nil {
		t.Fatalf("append audio: %v", err)
	}
	if state.AudioFrames != 1 || !state.LastActivity.Equal(now) {
		t.Fatalf("audio state = %+v", state)
	}

	state, err = manager.AppendText(&AppendTextParams{ThreadID: "thread-a", Text: "hello", Role: RoleAssistant})
	if err != nil {
		t.Fatalf("append text: %v", err)
	}
	if state.TextInputs != 1 {
		t.Fatalf("text state = %+v", state)
	}

	state, err = manager.AppendSpeech(&AppendSpeechParams{ThreadID: "thread-a", Text: "speak"})
	if err != nil {
		t.Fatalf("append speech: %v", err)
	}
	if state.SpeechInputs != 1 {
		t.Fatalf("speech state = %+v", state)
	}

	state, closed, err := manager.Stop(&StopParams{ThreadID: "thread-a"}, "client")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if state.ClosedAt == nil || closed.Method != NotificationClosed {
		t.Fatalf("closed = %+v notification=%+v", state, closed)
	}
	if _, err := manager.AppendText(&AppendTextParams{ThreadID: "thread-a", Text: "again"}); !errors.Is(err, ErrRealtimeNotRunning) {
		t.Fatalf("expected not running, got %v", err)
	}
}

func TestManagerZeroValueIsUsable(t *testing.T) {
	var manager Manager
	state, notifications, err := manager.Start(&StartParams{
		ThreadID:       "thread-a",
		OutputModality: OutputText,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if state == nil || state.Config.ThreadID != "thread-a" || state.Config.Version != VersionV2 {
		t.Fatalf("state = %+v", state)
	}
	if len(notifications) != 1 || notifications[0].Method != NotificationStarted {
		t.Fatalf("notifications = %+v", notifications)
	}
	state, err = manager.AppendText(&AppendTextParams{ThreadID: "thread-a", Text: "hello"})
	if err != nil {
		t.Fatalf("append text: %v", err)
	}
	if state.TextInputs != 1 {
		t.Fatalf("state after append = %+v", state)
	}
	closed, notification, err := manager.Stop(&StopParams{ThreadID: "thread-a"}, "client")
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if closed.ClosedAt == nil || notification.Method != NotificationClosed {
		t.Fatalf("closed = %+v notification=%+v", closed, notification)
	}
}

func TestManagerStreamsCodexHandoffByPhase(t *testing.T) {
	manager := NewManager()
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-a", OutputModality: OutputText}); err != nil {
		t.Fatalf("start: %v", err)
	}
	manager.BeginCodexOutput("thread-a", "commentary", "commentary")
	assertHandoffText(t, manager.StreamCodexOutput("thread-a", "commentary", "working"), "thinking", "working", false)

	manager.BeginCodexOutput("thread-a", "final", "final_answer")
	assertHandoffText(t, manager.StreamCodexOutput("thread-a", "final", "done"), "final", agentFinalMessagePrefix+"done", false)
	if notifications := manager.FinishCodexOutput("thread-a", "final"); len(notifications) != 0 {
		t.Fatalf("unexpected final notifications = %#v", notifications)
	}
	if notifications := manager.FinishCodexOutput("thread-a", "final"); len(notifications) != 0 {
		t.Fatalf("finished stream should only be removed once: %#v", notifications)
	}
}

func TestManagerBoundsCodexHandoffAndPreservesUTF8(t *testing.T) {
	manager := NewManager()
	if _, _, err := manager.Start(&StartParams{ThreadID: "thread-a", OutputModality: OutputText}); err != nil {
		t.Fatalf("start: %v", err)
	}
	manager.BeginCodexOutput("thread-a", "item-a", "commentary")
	input := strings.Repeat("界", 2000)
	first := manager.StreamCodexOutput("thread-a", "item-a", input)
	if len(first) != 1 {
		t.Fatalf("first notifications = %#v", first)
	}
	firstText := first[0].Params.(ItemAddedNotification).Item["text"].(string)
	if len(firstText) > codexOutputHeadLimit || !strings.HasPrefix(input, firstText) {
		t.Fatalf("first text bytes=%d valid prefix=%v", len(firstText), strings.HasPrefix(input, firstText))
	}
	finished := manager.FinishCodexOutput("thread-a", "item-a")
	if len(finished) != 1 {
		t.Fatalf("finish notifications = %#v", finished)
	}
	finishText := finished[0].Params.(ItemAddedNotification).Item["text"].(string)
	if !strings.HasPrefix(finishText, codexOutputTruncationText) || len(finishText) > len(codexOutputTruncationText)+codexOutputTailLimit {
		t.Fatalf("finish text bytes=%d", len(finishText))
	}
	if !strings.HasSuffix(input, strings.TrimPrefix(finishText, codexOutputTruncationText)) {
		t.Fatal("finish text does not preserve UTF-8 tail")
	}
}

func TestManagerSkipsAutomaticCodexHandoffModes(t *testing.T) {
	for _, field := range []string{"clientManagedHandoffs", "codexResponsesAsItems"} {
		t.Run(field, func(t *testing.T) {
			value := true
			params := &StartParams{ThreadID: field, OutputModality: OutputText}
			if field == "clientManagedHandoffs" {
				params.ClientManagedHandoffs = &value
			} else {
				params.CodexResponsesAsItems = &value
			}
			manager := NewManager()
			if _, _, err := manager.Start(params); err != nil {
				t.Fatalf("start: %v", err)
			}
			manager.BeginCodexOutput(field, "item", "final_answer")
			if notifications := manager.StreamCodexOutput(field, "item", "ignored"); len(notifications) != 0 {
				t.Fatalf("notifications = %#v", notifications)
			}
		})
	}
}

func assertHandoffText(t *testing.T, notifications []Notification, channel string, text string, final bool) {
	t.Helper()
	if len(notifications) != 1 {
		t.Fatalf("notifications = %#v", notifications)
	}
	params := notifications[0].Params.(ItemAddedNotification)
	if params.Item["type"] != "handoff_append" || params.Item["channel"] != channel || params.Item["text"] != text || params.Item["final"] != final {
		t.Fatalf("handoff item = %#v", params.Item)
	}
}

func TestNotificationFromEvent(t *testing.T) {
	audio := validAudio()
	cases := []struct {
		event Event
		want  NotificationMethod
	}{
		{Event{Type: "input_audio_buffer.speech_started", ItemID: "item-a"}, NotificationItemAdded},
		{Event{Type: "input_transcript.delta", Delta: "he"}, NotificationTranscriptDelta},
		{Event{Type: "input_transcript.done", Text: "hello"}, NotificationTranscriptDone},
		{Event{Type: "output_transcript.delta", Delta: "hi"}, NotificationTranscriptDelta},
		{Event{Type: "output_transcript.done", Text: "hello"}, NotificationTranscriptDone},
		{Event{Type: "audio.out", Audio: &audio}, NotificationOutputAudioDelta},
		{Event{Type: "response.cancelled", ResponseID: "resp"}, NotificationItemAdded},
		{Event{Type: "conversation.item.added", Item: map[string]any{"type": "message"}}, NotificationItemAdded},
		{Event{Type: "handoff.requested", HandoffID: "handoff", ItemID: "item"}, NotificationItemAdded},
		{Event{Type: "error", Message: "boom"}, NotificationError},
	}
	for _, tc := range cases {
		notification, ok := NotificationFromEvent("thread-a", tc.event)
		if !ok {
			t.Fatalf("event %s not mapped", tc.event.Type)
		}
		if notification.Method != tc.want {
			t.Fatalf("event %s method = %s, want %s", tc.event.Type, notification.Method, tc.want)
		}
	}
	if _, ok := NotificationFromEvent("thread-a", Event{Type: "session.updated"}); ok {
		t.Fatal("session.updated should be ignored")
	}
}

func TestRealtimeWireParserMatchesRustRequiredFieldsAndFallbacks(t *testing.T) {
	cases := []struct {
		name        string
		version     Version
		payload     string
		wantOK      bool
		wantType    string
		wantMessage string
		wantItemID  string
	}{
		{name: "session missing id", version: VersionV2, payload: `{"type":"session.updated","session":{}}`, wantOK: false},
		{name: "session", version: VersionV2, payload: `{"type":"session.updated","session":{"id":"sess-1","instructions":"hello"}}`, wantOK: true, wantType: "session.updated"},
		{name: "v2 session started unsupported", version: VersionV2, payload: `{"type":"session.started","session":{"id":"sess-1"}}`, wantOK: false},
		{name: "structured error", version: VersionV2, payload: `{"type":"error","error":{"code":"bad"}}`, wantOK: true, wantType: "error", wantMessage: `{"code":"bad"}`},
		{name: "error missing payload", version: VersionV2, payload: `{"type":"error"}`, wantOK: false},
		{name: "v1 audio missing rate", version: VersionV1, payload: `{"type":"conversation.output_audio.delta","delta":"AQID","channels":1}`, wantOK: false},
		{name: "v1 audio", version: VersionV1, payload: `{"type":"conversation.output_audio.delta","delta":"","sample_rate":24000,"channels":1}`, wantOK: true, wantType: "audio.out"},
		{name: "v2 audio defaults", version: VersionV2, payload: `{"type":"response.output_audio.delta","delta":"AQID"}`, wantOK: true, wantType: "audio.out"},
		{name: "v2 audio does not use v1 data fallback", version: VersionV2, payload: `{"type":"response.output_audio.delta","data":"AQID"}`, wantOK: false},
		{name: "v1 audio rejects fractional rate", version: VersionV1, payload: `{"type":"conversation.output_audio.delta","delta":"AQID","sample_rate":24000.5,"channels":1}`, wantOK: false},
		{name: "v1 rejects v2 audio event", version: VersionV1, payload: `{"type":"response.audio.delta","delta":"AQID"}`, wantOK: false},
		{name: "transcript missing delta", version: VersionV2, payload: `{"type":"response.output_text.delta"}`, wantOK: false},
		{name: "empty transcript delta", version: VersionV2, payload: `{"type":"response.output_text.delta","delta":""}`, wantOK: true, wantType: "output_transcript.delta"},
		{name: "v3 empty transcript delta", version: VersionV3, payload: `{"type":"input_transcript.added","item":{"text":""}}`, wantOK: true, wantType: "input_transcript.delta"},
		{name: "ordinary item done", version: VersionV1, payload: `{"type":"conversation.item.done","item":{"id":"item-done","type":"message"}}`, wantOK: true, wantType: "conversation.item.done", wantItemID: "item-done"},
		{name: "empty item id", version: VersionV1, payload: `{"type":"conversation.item.done","item":{"id":""}}`, wantOK: true, wantType: "conversation.item.done"},
		{name: "v1 rejects v2 response lifecycle", version: VersionV1, payload: `{"type":"response.created"}`, wantOK: false},
		{name: "v2 rejects v1 direct handoff", version: VersionV2, payload: `{"type":"conversation.handoff.requested","handoff_id":"h","item_id":"i","input_transcript":"x"}`, wantOK: false},
		{name: "v2 empty function call id", version: VersionV2, payload: `{"type":"conversation.item.done","item":{"type":"function_call","name":"background_agent","call_id":"","arguments":""}}`, wantOK: true, wantType: "handoff.requested"},
		{name: "v3 delegation missing content", version: VersionV3, payload: `{"type":"delegation.created","item":{"type":"delegation","target":"client","id":"delegation-1"}}`, wantOK: false},
		{name: "v3 delegation", version: VersionV3, payload: `{"type":"delegation.created","item":{"type":"delegation","target":"client","id":"delegation-1","content":[]}}`, wantOK: true, wantType: "handoff.requested", wantItemID: "delegation-1"},
		{name: "v3 empty delegation id", version: VersionV3, payload: `{"type":"delegation.created","item":{"type":"delegation","target":"client","id":"","content":[]}}`, wantOK: true, wantType: "handoff.requested"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			event, ok := parseRealtimeWireEvent(tc.version, []byte(tc.payload))
			if ok != tc.wantOK {
				t.Fatalf("parse ok = %v, want %v; event = %+v", ok, tc.wantOK, event)
			}
			if !tc.wantOK {
				return
			}
			if event.Type != tc.wantType || event.Message != tc.wantMessage || event.ItemID != tc.wantItemID {
				t.Fatalf("event = %+v", event)
			}
			if tc.name == "session" && (event.RealtimeSessionID != "sess-1" || event.Instructions == nil || *event.Instructions != "hello") {
				t.Fatalf("session event = %+v", event)
			}
			if tc.name == "v2 audio defaults" && (event.Audio == nil || event.Audio.SampleRate != 24000 || event.Audio.NumChannels != 1) {
				t.Fatalf("v2 audio event = %+v", event)
			}
		})
	}
}

func TestAppendValidations(t *testing.T) {
	if err := (&AppendTextParams{ThreadID: "thread-a", Role: "weird", Text: "x"}).Validate(); !errors.Is(err, ErrInvalidRealtimeRequest) {
		t.Fatalf("expected invalid role, got %v", err)
	}
	if err := WebRTCTransport("").Validate(); err != nil {
		t.Fatalf("empty but present SDP should be valid, got %v", err)
	}
}

func validAudio() AudioChunk {
	return AudioChunk{
		Data:        base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		SampleRate:  24000,
		NumChannels: 1,
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 6, 29, 8, 0, 0, 0, time.UTC)
}
