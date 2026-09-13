package app

import (
	"context"
	"testing"

	"codex_go/appserver"
	"codex_go/config"
	"codex_go/realtime"
	codextea "codex_go/tui/tea"
)

type recordingInteractiveVoiceRouter struct {
	requests []*appserver.Request
}

func (r *recordingInteractiveVoiceRouter) Handle(request *appserver.Request) *appserver.Response {
	r.requests = append(r.requests, request)
	switch request.Method {
	case appserver.MethodInitialize:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &appserver.InitializeResponse{}}
	case appserver.MethodThreadRealtimeListVoices:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &realtime.ListVoicesResponse{
			Voices: realtime.VoicesList{V1: []realtime.Voice{"maple", "juniper"}, DefaultV1: "maple"},
		}}
	case appserver.MethodConfigRead:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &config.ConfigReadResponse{
			Config: map[string]any{"realtime": map[string]any{"voice": "juniper"}},
		}}
	case appserver.MethodThreadRealtimeStart, appserver.MethodThreadRealtimeStop, appserver.MethodThreadRealtimeAppendSpeech:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &realtime.ListVoicesResponse{}}
	case appserver.MethodConfigBatchWrite:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Result: &config.ConfigWriteResponse{}}
	default:
		return &appserver.Response{JSONRPC: "2.0", ID: request.ID, Error: &appserver.ResponseError{Code: -32601, Message: "unexpected method"}}
	}
}

func TestInteractiveLocalVoiceCallbacksUseAppServerProtocol(t *testing.T) {
	router := &recordingInteractiveVoiceRouter{}
	settings, voices, start, stop := interactiveLocalVoiceCallbacks(func() interactiveVoiceRouter { return router })

	if got := settings(context.Background()); !got.VoiceSet || got.Voice != "juniper" {
		t.Fatalf("settings = %#v, want juniper", got)
	}
	catalog := voices(context.Background())
	if len(catalog.V1) != 2 || catalog.DefaultV1 != "maple" {
		t.Fatalf("voices = %#v", catalog)
	}
	if err := start(context.Background(), realtime.StartParams{ThreadID: "thread-1"}); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := stop(context.Background(), " thread-1 "); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// The session initializes the connection once, so the callbacks issue only
	// their own request; a per-request initialize would fail as already
	// initialized.
	wantMethods := []appserver.Method{
		appserver.MethodConfigRead,
		appserver.MethodThreadRealtimeListVoices,
		appserver.MethodThreadRealtimeStart,
		appserver.MethodThreadRealtimeStop,
	}
	if len(router.requests) != len(wantMethods) {
		t.Fatalf("requests = %d, want %d", len(router.requests), len(wantMethods))
	}
	for index, method := range wantMethods {
		if router.requests[index].Method != method {
			t.Fatalf("request[%d] = %s, want %s", index, router.requests[index].Method, method)
		}
		if router.requests[index].ConnectionID != interactiveVoiceConnectionID {
			t.Fatalf("request[%d] connection = %q", index, router.requests[index].ConnectionID)
		}
	}
}

func TestInteractiveLocalVoiceSettingsFallsBackToBuiltinCatalog(t *testing.T) {
	settings := interactiveLocalVoiceSettings(func() interactiveVoiceRouter { return nil })
	message := settings()()
	result, ok := message.(codextea.VoiceSettingsMsg)
	if !ok {
		t.Fatalf("settings message = %T", message)
	}
	if len(result.Voices) == 0 || result.Current == "" {
		t.Fatalf("builtin settings = %#v", result)
	}
}

// A nil factory must degrade to a reported error, not a nil function call: the
// embedded TUI wires these hooks before a voice session exists.
func TestInteractiveLocalVoiceHooksTolerateNilFactory(t *testing.T) {
	message := interactiveLocalVoiceSaver(nil)("maple")()
	if saved, ok := message.(codextea.VoiceSavedMsg); !ok || saved.Err == nil {
		t.Fatalf("nil-factory save = %#v, want an error", message)
	}

	message = interactiveLocalSpeechSender(nil, func() string { return "thread-1" })("item-1", "hello")()
	if spoken, ok := message.(codextea.VoiceSpeechResultMsg); !ok || spoken.Err == nil {
		t.Fatalf("nil-factory speech = %#v, want an error", message)
	}
}

func TestInteractiveLocalVoiceSaverPersistsThroughRouter(t *testing.T) {
	router := &recordingInteractiveVoiceRouter{}
	message := interactiveLocalVoiceSaver(func() interactiveVoiceRouter { return router })("juniper")()
	saved, ok := message.(codextea.VoiceSavedMsg)
	if !ok {
		t.Fatalf("save message = %T", message)
	}
	if saved.Err != nil || saved.Voice != "juniper" {
		t.Fatalf("save = %#v, want juniper without error", saved)
	}
	wantMethods := []appserver.Method{appserver.MethodConfigBatchWrite, appserver.MethodConfigRead}
	if len(router.requests) != len(wantMethods) {
		t.Fatalf("requests = %d, want %d", len(router.requests), len(wantMethods))
	}
	for index, method := range wantMethods {
		if router.requests[index].Method != method {
			t.Fatalf("request[%d] = %s, want %s", index, router.requests[index].Method, method)
		}
	}
}

func TestInteractiveLocalSpeechSenderPostsThroughRouter(t *testing.T) {
	router := &recordingInteractiveVoiceRouter{}
	message := interactiveLocalSpeechSender(
		func() interactiveVoiceRouter { return router },
		func() string { return " thread-1 " },
	)("item-1", "hello")()
	spoken, ok := message.(codextea.VoiceSpeechResultMsg)
	if !ok {
		t.Fatalf("speech message = %T", message)
	}
	if spoken.Err != nil || spoken.ItemID != "item-1" {
		t.Fatalf("speech = %#v, want item-1 without error", spoken)
	}
	if len(router.requests) != 1 || router.requests[0].Method != appserver.MethodThreadRealtimeAppendSpeech {
		t.Fatalf("requests = %#v", router.requests)
	}
	if router.requests[0].ConnectionID != interactiveVoiceConnectionID {
		t.Fatalf("connection = %q", router.requests[0].ConnectionID)
	}
}

// TestRealtimeNotificationMessageMapsWirePayloads covers the sink bridge the
// local session uses to forward realtime notifications to the TUI.
func TestRealtimeNotificationMessageMapsWirePayloads(t *testing.T) {
	message, ok := realtimeNotificationMessage(appserver.NewNotification(
		appserver.NotificationThreadRealtimeSDP,
		appserver.ThreadRealtimeSDPNotification{SDP: "v=0"},
	))
	if !ok {
		t.Fatal("SDP notification was not mapped")
	}
	if message.Notification.Text != "v=0" {
		t.Fatalf("mapped message = %#v", message)
	}
	if _, ok := realtimeNotificationMessage(appserver.NewNotification(appserver.NotificationTurnStarted, struct{}{})); ok {
		t.Fatal("unrelated notification was mapped to a voice message")
	}
	if _, ok := realtimeNotificationMessage(nil); ok {
		t.Fatal("nil notification was mapped")
	}
}
