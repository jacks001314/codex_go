package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/realtime"
	"codex_go/session"
	"codex_go/turn"

	"github.com/coder/websocket"
)

func TestRealtimeStartPreflightFailureReturnsResponseAndEmitsErrorOnlyLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[features]\nrealtime_conversation = true\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	t.Setenv(auth.OpenAIAPIKeyEnv, "")

	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeStart, map[string]any{
		"threadId":       "thread-preflight",
		"outputModality": "audio",
	}))
	if response.Error != nil {
		t.Fatalf("thread/realtime/start returned JSON-RPC error: %+v", response.Error)
	}

	errorNotification := realtimeNotification[*ThreadRealtimeErrorNotification](t, sink, NotificationThreadRealtimeError)
	if errorNotification.ThreadID != "thread-preflight" || errorNotification.Message != "realtime conversation requires API key auth" {
		t.Fatalf("thread/realtime/error = %+v", errorNotification)
	}

	time.Sleep(100 * time.Millisecond)
	for _, notification := range sink.List() {
		if notification.Method == NotificationThreadRealtimeStarted || notification.Method == NotificationThreadRealtimeClosed {
			t.Fatalf("preflight failure emitted %s: %#v", notification.Method, notification.Params)
		}
	}
}

func TestRealtimeStartOptionsUseThreadConfigAuthAndRustDefaults(t *testing.T) {
	home := t.TempDir()
	configBody := `experimental_realtime_ws_base_url = "https://realtime.example.test/custom/v1"
experimental_realtime_webrtc_call_base_url = "https://calls.example.test/backend-api/codex"
experimental_realtime_ws_model = "realtime-test-model"
experimental_realtime_ws_backend_prompt = "custom backend prompt"
experimental_realtime_ws_startup_context = "startup context"

[features]
realtime_conversation = true

[realtime]
version = "v3"
type = "transcription"
voice = "cove"
`
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	t.Setenv(auth.OpenAIAPIKeyEnv, "sk-realtime-test")

	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home)})
	t.Cleanup(func() { _ = router.Close() })
	options, err := router.realtimeStartOptions(&realtime.StartParams{
		ThreadID:       "thread-config",
		OutputModality: realtime.OutputAudio,
	})
	if err != nil {
		t.Fatalf("realtimeStartOptions: %v", err)
	}
	if options == nil || options.Backend == nil {
		t.Fatal("realtimeStartOptions returned no backend")
	}
	if options.DefaultModel != "realtime-test-model" || options.DefaultVersion != realtime.VersionV3 || options.DefaultVoice != realtime.VoiceCove || options.SessionMode != realtime.SessionModeTranscription {
		t.Fatalf("realtime defaults = model %q version %q voice %q type %q", options.DefaultModel, options.DefaultVersion, options.DefaultVoice, options.SessionMode)
	}
	if options.Instructions == nil || *options.Instructions != "custom backend prompt\n\nstartup context" {
		t.Fatalf("realtime instructions = %#v", options.Instructions)
	}
	if options.Backend.WebsocketBaseURL != "https://realtime.example.test/custom/v1" || options.Backend.SidebandBaseURL != options.Backend.WebsocketBaseURL || options.Backend.WebRTCCallBaseURL != "https://calls.example.test/backend-api/codex" {
		t.Fatalf("realtime backend URLs = %+v", options.Backend)
	}
	for key, want := range map[string]string{
		"Authorization": "Bearer sk-realtime-test",
		"session-id":    "thread-config",
		"thread-id":     "thread-config",
		"originator":    defaultInitializeOriginator,
	} {
		if got := options.Backend.Headers.Get(key); got != want {
			t.Fatalf("websocket header %s = %q, want %q", key, got, want)
		}
	}
	request, err := http.NewRequest(http.MethodPost, options.Backend.WebRTCCallBaseURL, strings.NewReader("offer"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header = options.Backend.CallHeaders.Clone()
	if err := options.Backend.PrepareCall(context.Background(), request, []byte("offer")); err != nil {
		t.Fatalf("PrepareCall: %v", err)
	}
	if got := request.Header.Get("Authorization"); got != "Bearer sk-realtime-test" {
		t.Fatalf("WebRTC Authorization = %q", got)
	}
}

func TestRealtimeV3HandoffStartsTurnAndWritesCodexOutputBack(t *testing.T) {
	modelRequests := make(chan map[string]any, 1)
	modelServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode model request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		modelRequests <- body
		response.Header().Set("Content-Type", "text/event-stream")
		_, _ = response.Write([]byte(modelResponsesSSE(
			`{"type":"response.created","response":{"id":"resp-realtime"}}`,
			`{"type":"response.output_item.added","item":{"id":"msg-realtime","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`,
			`{"type":"response.output_text.delta","item_id":"msg-realtime","delta":"[FINAL]agent done"}`,
			`{"type":"response.output_item.done","item":{"id":"msg-realtime","type":"message","role":"assistant","content":[{"type":"output_text","text":"[FINAL]agent done"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-realtime","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		)))
	}))
	defer modelServer.Close()

	realtimeOutbound := make(chan map[string]any, 4)
	realtimeServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(request.Context()); err != nil {
			return
		}
		if err := conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"session.started","session":{"id":"sess-appserver"}}`)); err != nil {
			return
		}
		if err := conn.Write(request.Context(), websocket.MessageText, []byte(`{"type":"delegation.created","item":{"type":"delegation","target":"client","id":"delegation-appserver","content":[{"type":"input_text","text":"inspect the workspace"}]}}`)); err != nil {
			return
		}
		for {
			_, payload, err := conn.Read(request.Context())
			if err != nil {
				return
			}
			var message map[string]any
			if json.Unmarshal(payload, &message) == nil {
				realtimeOutbound <- message
			}
		}
	}))
	defer realtimeServer.Close()

	home := t.TempDir()
	configBody := "experimental_realtime_ws_base_url = " + strconv.Quote(realtimeServer.URL) + "\n" +
		"experimental_realtime_ws_startup_context = \"\"\n\n" +
		"[features]\nrealtime_conversation = true\n\n" +
		"[realtime]\nversion = \"v3\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	t.Setenv(auth.OpenAIAPIKeyEnv, "sk-realtime-e2e")

	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	agent := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{BaseURL: modelServer.URL + "/v1"},
		Stream:   true,
	})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
		Config:       config.NewConfigService(home),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread/start: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	start := router.Handle(requestWithParams(t, IntID(2), MethodThreadRealtimeStart, map[string]any{
		"threadId":                        threadID,
		"outputModality":                  "audio",
		"version":                         "v3",
		"codexResponseHandoffMode":        "bemTags",
		"includeStartupContext":           false,
		"flushTranscriptTailOnSessionEnd": true,
	}))
	if start.Error != nil {
		t.Fatalf("thread/realtime/start: %+v", start.Error)
	}
	started := realtimeNotification[*ThreadRealtimeStartedNotification](t, sink, NotificationThreadRealtimeStarted)
	if started.ThreadID != threadID || started.Version != "v3" {
		t.Fatalf("thread/realtime/started = %+v", started)
	}

	select {
	case request := <-modelRequests:
		if !nestedValueContains(request, "<realtime_delegation>") || !nestedValueContains(request, "inspect the workspace") || !nestedValueContains(request, defaultRealtimeStartInstructions) {
			encoded, _ := json.Marshal(request)
			t.Fatalf("model request did not contain realtime delegation and start world state: %s", encoded)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("realtime handoff did not start a model turn")
	}

	message := waitAppserverRealtimeOutbound(t, realtimeOutbound)
	if message["type"] != "delegation.context.append" || message["delegation_item_id"] != "delegation-appserver" || message["channel"] != "speakable" {
		t.Fatalf("Codex realtime output envelope = %#v", message)
	}
	content, ok := message["content"].([]any)
	if !ok || len(content) != 1 || content[0].(map[string]any)["text"] != "[FINAL]agent done" {
		t.Fatalf("Codex realtime output content = %#v", message["content"])
	}
	waitRealtimeTurnCompleted(t, sink, threadID)
	persisted, err := store.Load(session.ThreadID(threadID))
	if err != nil {
		t.Fatalf("load realtime thread: %v", err)
	}
	if !recordHasRetainedRealtimeStart(persisted) {
		t.Fatal("successful realtime turn did not retain hidden start world-state context")
	}

	stop := router.Handle(requestWithParams(t, IntID(3), MethodThreadRealtimeStop, map[string]any{"threadId": threadID}))
	if stop.Error != nil {
		t.Fatalf("thread/realtime/stop: %+v", stop.Error)
	}
}

func TestRealtimeHandoffSteersActiveTurnInsteadOfStartingAnother(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread/start: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	router.handleRealtimeEvent(threadID, realtime.Event{
		Type:            "handoff.requested",
		HandoffID:       "delegation-first",
		InputTranscript: "start the long-running task",
	})
	waitForBlockingAgentStart(t, agent)
	active := router.activeRuntimeTurnSnapshot(threadID)
	if active == nil || strings.TrimSpace(active.ID) == "" {
		t.Fatal("first realtime handoff did not create an active turn")
	}

	router.handleRealtimeEvent(threadID, realtime.Event{
		Type:            "handoff.requested",
		HandoffID:       "delegation-second",
		InputTranscript: "change direction while the turn is active",
	})

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read realtime thread: %v", err)
	}
	var steered *session.Item
	for index := range record.Items {
		item := &record.Items[index]
		if item.Metadata["steered"] == true && strings.Contains(item.Text, "change direction while the turn is active") {
			steered = item
			break
		}
	}
	if steered == nil {
		t.Fatalf("second realtime handoff was not persisted as steer: %#v", record.Items)
	}
	if steered.Metadata["turnId"] != active.ID {
		t.Fatalf("steered turnId = %#v, want %q", steered.Metadata["turnId"], active.ID)
	}

	startedCount := 0
	for _, notification := range sink.List() {
		if notification.Method != NotificationTurnStarted {
			continue
		}
		started, ok := notification.Params.(*TurnStartedNotification)
		if ok && started != nil && started.ThreadID == threadID {
			startedCount++
		}
	}
	if startedCount != 1 {
		t.Fatalf("turn/started count = %d, want 1", startedCount)
	}

	interrupt := router.Handle(requestWithParams(t, IntID(2), MethodTurnInterrupt, turn.TurnInterruptParams{
		ThreadID: threadID,
		TurnID:   active.ID,
	}))
	if interrupt.Error != nil {
		t.Fatalf("turn/interrupt: %+v", interrupt.Error)
	}
	waitForTurnCompletedStatus(t, sink, active.ID, TurnStatusInterrupted)
}

func TestConcurrentRealtimeHandoffsCreateOneTurnAndOneSteer(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newBlockingAgent()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)
	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if threadStart.Error != nil {
		t.Fatalf("thread/start: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID

	var wait sync.WaitGroup
	for _, input := range []string{"concurrent handoff one", "concurrent handoff two"} {
		input := input
		wait.Add(1)
		go func() {
			defer wait.Done()
			router.handleRealtimeEvent(threadID, realtime.Event{Type: "handoff.requested", InputTranscript: input})
		}()
	}
	wait.Wait()
	waitForBlockingAgentStart(t, agent)
	active := router.activeRuntimeTurnSnapshot(threadID)
	if active == nil {
		t.Fatal("concurrent realtime handoffs did not create an active turn")
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read realtime thread: %v", err)
	}
	steerCount := 0
	for index := range record.Items {
		if record.Items[index].Metadata["steered"] == true {
			steerCount++
		}
	}
	startedCount := 0
	for _, notification := range sink.List() {
		if notification.Method == NotificationTurnStarted {
			started, ok := notification.Params.(*TurnStartedNotification)
			if ok && started != nil && started.ThreadID == threadID {
				startedCount++
			}
		}
	}
	if startedCount != 1 || steerCount != 1 {
		t.Fatalf("concurrent realtime routing produced %d turns and %d steers", startedCount, steerCount)
	}
	interrupt := router.Handle(requestWithParams(t, IntID(2), MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: threadID, TurnID: active.ID}))
	if interrupt.Error != nil {
		t.Fatalf("turn/interrupt: %+v", interrupt.Error)
	}
	waitForTurnCompletedStatus(t, sink, active.ID, TurnStatusInterrupted)
}

func TestRealtimeOperationsQueueBehindStartAndReturnImmediately(t *testing.T) {
	handshakeGate := make(chan struct{})
	handshakeStarted := make(chan struct{})
	outbound := make(chan map[string]any, 8)
	var releaseOnce sync.Once
	releaseHandshake := func() { releaseOnce.Do(func() { close(handshakeGate) }) }
	t.Cleanup(releaseHandshake)

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		select {
		case <-handshakeStarted:
		default:
			close(handshakeStarted)
		}
		<-handshakeGate
		conn, err := websocket.Accept(response, request, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
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

	manager := realtime.NewManager()
	manager.SetTransportBackend(&realtime.TransportBackendConfig{WebsocketBaseURL: server.URL})
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{Realtime: manager})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeStart, map[string]any{
		"threadId": "thread-queued", "outputModality": "text", "version": "v2",
	}))
	if start.Error != nil {
		t.Fatalf("thread/realtime/start: %+v", start.Error)
	}
	select {
	case <-handshakeStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("realtime start did not begin websocket handshake")
	}

	appendText := router.Handle(requestWithParams(t, IntID(2), MethodThreadRealtimeAppendText, map[string]any{
		"threadId": "thread-queued", "text": "queued behind start",
	}))
	if appendText.Error != nil {
		t.Fatalf("thread/realtime/appendText: %+v", appendText.Error)
	}
	stop := router.Handle(requestWithParams(t, IntID(3), MethodThreadRealtimeStop, map[string]any{"threadId": "thread-queued"}))
	if stop.Error != nil {
		t.Fatalf("thread/realtime/stop: %+v", stop.Error)
	}

	releaseHandshake()
	realtimeNotification[*ThreadRealtimeStartedNotification](t, sink, NotificationThreadRealtimeStarted)
	realtimeNotification[*ThreadRealtimeClosedNotification](t, sink, NotificationThreadRealtimeClosed)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case message := <-outbound:
			if message["type"] != "conversation.item.create" {
				continue
			}
			if !nestedValueContains(message, "queued behind start") {
				t.Fatalf("queued text message = %#v", message)
			}
			return
		case <-deadline:
			t.Fatal("queued appendText was not sent after realtime start")
		}
	}
}

func TestRealtimeAudioDropsWhenPerThreadQueueIsFullLikeRust(t *testing.T) {
	manager := realtime.NewManager()
	if _, _, err := manager.Start(&realtime.StartParams{ThreadID: "thread-audio-backpressure", OutputModality: realtime.OutputText}); err != nil {
		t.Fatalf("start realtime manager: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{Realtime: manager})
	t.Cleanup(func() { _ = router.Close() })

	blocked := make(chan struct{})
	release := make(chan struct{})
	if !router.enqueueRealtimeOperation("thread-audio-backpressure", func(context.Context) {
		close(blocked)
		<-release
	}) {
		t.Fatal("enqueue blocking operation")
	}
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("operation worker did not block")
	}
	for i := 0; i < realtimeOperationQueueCapacity; i++ {
		if !router.enqueueRealtimeOperation("thread-audio-backpressure", func(context.Context) {}) {
			t.Fatalf("fill queue at operation %d", i)
		}
	}

	router.appendRealtimeAudioAsync(realtime.AppendAudioParams{
		ThreadID: "thread-audio-backpressure",
		Audio: realtime.AudioChunk{
			Data:        "AQID",
			SampleRate:  24_000,
			NumChannels: 1,
		},
	})
	close(release)
	drained := make(chan struct{})
	if !router.enqueueRealtimeOperation("thread-audio-backpressure", func(context.Context) { close(drained) }) {
		t.Fatal("enqueue drain marker")
	}
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("operation queue did not drain")
	}

	state, ok := manager.State("thread-audio-backpressure")
	if !ok || state.AudioFrames != 0 {
		t.Fatalf("dropped audio changed realtime state: %#v", state)
	}
}

func TestRealtimeOperationsBeforeStartUseRustAsyncErrorAndCloseSemantics(t *testing.T) {
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)

	appendText := router.Handle(requestWithParams(t, IntID(1), MethodThreadRealtimeAppendText, map[string]any{
		"threadId": "thread-not-running", "text": "hello",
	}))
	if appendText.Error != nil {
		t.Fatalf("append before start returned JSON-RPC error: %+v", appendText.Error)
	}
	operationError := waitRealtimeOperationError(t, sink, "thread-not-running")
	if operationError.Error.Message != "conversation is not running" || operationError.Error.CodexErrorInfo != CodexErrorInfo("badRequest") || operationError.WillRetry || operationError.TurnID != "" {
		t.Fatalf("append before start error = %+v", operationError)
	}

	stop := router.Handle(requestWithParams(t, IntID(2), MethodThreadRealtimeStop, map[string]any{"threadId": "thread-not-running"}))
	if stop.Error != nil {
		t.Fatalf("stop before start returned JSON-RPC error: %+v", stop.Error)
	}
	closed := realtimeNotification[*ThreadRealtimeClosedNotification](t, sink, NotificationThreadRealtimeClosed)
	if closed.ThreadID != "thread-not-running" || closed.Reason == nil || *closed.Reason != "requested" {
		t.Fatalf("stop before start closed = %+v", closed)
	}
}

func TestRealtimeWorldStateEmitsStartAndEndTransitionsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	for _, threadID := range []string{"thread-world-state", "thread-world-state-custom"} {
		if err := store.Save(&session.Record{ID: session.ThreadID(threadID), Metadata: session.Metadata{CWD: t.TempDir()}}); err != nil {
			t.Fatalf("save %s: %v", threadID, err)
		}
	}
	manager := realtime.NewManager()
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Realtime: manager})
	t.Cleanup(func() { _ = router.Close() })

	startParams := &realtime.StartParams{ThreadID: "thread-world-state", OutputModality: realtime.OutputText}
	if _, _, err := manager.Start(startParams); err != nil {
		t.Fatalf("start realtime state: %v", err)
	}
	startItem, err := router.realtimeWorldStateInputItem("thread-world-state", &config.Config{Values: map[string]any{}})
	if err != nil {
		t.Fatalf("realtime start world state: %v", err)
	}
	if !nestedValueContains(startItem, "<realtime_conversation>") || !nestedValueContains(startItem, defaultRealtimeStartInstructions) {
		t.Fatalf("realtime start world-state item = %#v", startItem)
	}
	retainedStart, ok := realtimeWorldStateSessionItemForTurn("turn-world-state", startItem, time.Now().UTC())
	if !ok {
		t.Fatalf("start world-state item was not persistable: %#v", startItem)
	}
	if _, err := store.AppendItem("thread-world-state", retainedStart); err != nil {
		t.Fatalf("persist retained start: %v", err)
	}
	unchanged, err := router.realtimeWorldStateInputItem("thread-world-state", &config.Config{Values: map[string]any{}})
	if err != nil || unchanged != nil {
		t.Fatalf("unchanged active realtime state = %#v, %v", unchanged, err)
	}
	if _, _, err := manager.Stop(&realtime.StopParams{ThreadID: "thread-world-state"}, "requested"); err != nil {
		t.Fatalf("stop realtime state: %v", err)
	}
	endItem, err := router.realtimeWorldStateInputItem("thread-world-state", &config.Config{Values: map[string]any{}})
	if err != nil {
		t.Fatalf("realtime end world state: %v", err)
	}
	if !nestedValueContains(endItem, defaultRealtimeEndInstructions) || !nestedValueContains(endItem, "Reason: inactive") {
		t.Fatalf("realtime end world-state item = %#v", endItem)
	}
	unchanged, err = router.realtimeWorldStateInputItem("thread-world-state", &config.Config{Values: map[string]any{}})
	if err != nil || unchanged != nil {
		t.Fatalf("unchanged inactive realtime state = %#v, %v", unchanged, err)
	}

	customParams := &realtime.StartParams{ThreadID: "thread-world-state-custom", OutputModality: realtime.OutputText}
	if _, _, err := manager.Start(customParams); err != nil {
		t.Fatalf("start custom realtime state: %v", err)
	}
	customItem, err := router.realtimeWorldStateInputItem("thread-world-state-custom", &config.Config{Values: map[string]any{
		"experimental_realtime_start_instructions": "custom realtime executor instructions",
	}})
	if err != nil {
		t.Fatalf("custom realtime world state: %v", err)
	}
	if !nestedValueContains(customItem, "custom realtime executor instructions") || nestedValueContains(customItem, defaultRealtimeStartInstructions) {
		t.Fatalf("custom realtime start item = %#v", customItem)
	}
	restated, err := router.realtimeWorldStateInputItem("thread-world-state-custom", &config.Config{Values: map[string]any{
		"experimental_realtime_start_instructions": "custom realtime executor instructions",
	}})
	if err != nil || !nestedValueContains(restated, "custom realtime executor instructions") {
		t.Fatalf("active state without retained start was not restated: %#v, %v", restated, err)
	}
}

func TestRealtimeWorldStateDoesNotEmitEndWhenStartWasNotRetained(t *testing.T) {
	store := session.NewStore(t.TempDir())
	rawState, err := session.EncodeWorldState(&session.WorldState{RealtimeConversation: json.RawMessage(`{"active":true}`)})
	if err != nil {
		t.Fatalf("encode world state: %v", err)
	}
	threadID := session.ThreadID("thread-realtime-unseen-start")
	if err := store.Save(&session.Record{ID: threadID, Metadata: session.Metadata{CWD: t.TempDir(), WorldState: rawState}}); err != nil {
		t.Fatalf("save thread: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Realtime: realtime.NewManager()})
	t.Cleanup(func() { _ = router.Close() })

	item, err := router.realtimeWorldStateInputItem(string(threadID), &config.Config{Values: map[string]any{}})
	if err != nil {
		t.Fatalf("render inactive realtime state: %v", err)
	}
	if item != nil {
		t.Fatalf("unseen realtime start produced an end fragment: %#v", item)
	}
}

func nestedValueContains(value any, needle string) bool {
	switch typed := value.(type) {
	case string:
		return strings.Contains(typed, needle)
	case []any:
		for _, item := range typed {
			if nestedValueContains(item, needle) {
				return true
			}
		}
	case []map[string]any:
		for _, item := range typed {
			if nestedValueContains(item, needle) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if nestedValueContains(item, needle) {
				return true
			}
		}
	}
	return false
}

func waitRealtimeTurnCompleted(t *testing.T, sink *NotificationBuffer, threadID string) *TurnCompletedNotification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationTurnCompleted {
				continue
			}
			completed, ok := notification.Params.(*TurnCompletedNotification)
			if ok && completed != nil && completed.ThreadID == threadID && completed.Turn.Status == TurnStatusCompleted {
				return completed
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for realtime turn completion on %s", threadID)
	return nil
}

func waitAppserverRealtimeOutbound(t *testing.T, outbound <-chan map[string]any) map[string]any {
	t.Helper()
	select {
	case message := <-outbound:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for app-server realtime output")
		return nil
	}
}

func waitRealtimeOperationError(t *testing.T, sink *NotificationBuffer, threadID string) *ErrorNotification {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, notification := range sink.List() {
			if notification.Method != NotificationError {
				continue
			}
			operationError, ok := notification.Params.(*ErrorNotification)
			if ok && operationError != nil && operationError.ThreadID == threadID {
				return operationError
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for realtime operation error on %s", threadID)
	return nil
}
