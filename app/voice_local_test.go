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
	closed   int
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

func (r *recordingInteractiveVoiceRouter) Close() error {
	r.closed++
	return nil
}

func TestInteractiveLocalVoiceCallbacksUseAppServerProtocol(t *testing.T) {
	router := &recordingInteractiveVoiceRouter{}
	settings, voices, start, stop := interactiveLocalVoiceCallbacks(func() interactiveGoalRouter { return router })

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

	wantPairs := []struct {
		init appserver.Method
		op   appserver.Method
	}{
		{appserver.MethodInitialize, appserver.MethodConfigRead},
		{appserver.MethodInitialize, appserver.MethodThreadRealtimeListVoices},
		{appserver.MethodInitialize, appserver.MethodThreadRealtimeStart},
		{appserver.MethodInitialize, appserver.MethodThreadRealtimeStop},
	}
	if len(router.requests) != 2*len(wantPairs) {
		t.Fatalf("requests = %d, want %d", len(router.requests), 2*len(wantPairs))
	}
	for index, pair := range wantPairs {
		if router.requests[2*index].Method != pair.init || router.requests[2*index+1].Method != pair.op {
			t.Fatalf("request[%d]=%s request[%d]=%s", 2*index, router.requests[2*index].Method, 2*index+1, router.requests[2*index+1].Method)
		}
		for _, position := range []int{2 * index, 2*index + 1} {
			if router.requests[position].ConnectionID != interactiveVoiceConnectionID {
				t.Fatalf("request[%d] connection = %q", position, router.requests[position].ConnectionID)
			}
		}
	}
}

func TestInteractiveLocalVoiceSettingsFallsBackToBuiltinCatalog(t *testing.T) {
	settings := interactiveLocalVoiceSettings(func() interactiveGoalRouter { return nil })
	message := settings()()
	result, ok := message.(codextea.VoiceSettingsMsg)
	if !ok {
		t.Fatalf("settings message = %T", message)
	}
	if len(result.Voices) == 0 || result.Current == "" {
		t.Fatalf("builtin settings = %#v", result)
	}
}
