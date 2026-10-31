package appserver

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #58434: provider audio validation failures are non-retryable input
// errors. Rust maps CodexErrorDetails::InvalidAudioInput to
// CodexErrorInfo::BadRequest, keeps the invalid_audio_input analytics kind and
// reports no HTTP status (http_status_code_value returns None).
func TestTurnAnalyticsErrorFieldsClassifyInvalidAudioInputLikeRust(t *testing.T) {
	for _, status := range []int{0, http.StatusBadRequest} {
		fields := turnAnalyticsErrorFieldsFromAPIError(&codexapi.APIError{
			Kind:    codexapi.ErrorInvalidAudioInput,
			Status:  status,
			Message: "invalid audio input: [redacted]",
		})
		if fields.TurnError != "badRequest" {
			t.Fatalf("status %d codex error info = %q, want badRequest", status, fields.TurnError)
		}
		if fields.CodexErrorKind == nil || *fields.CodexErrorKind != "invalid_audio_input" {
			t.Fatalf("status %d analytics kind = %#v, want invalid_audio_input", status, fields.CodexErrorKind)
		}
		if fields.HTTPStatusCode != nil {
			t.Fatalf("status %d analytics http status = %d, want none", status, *fields.HTTPStatusCode)
		}
	}
}

func TestRuntimeRouterTurnStartFailedAnalyticsClassifiesInvalidAudioInputLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	store := session.NewStore(filepath.Join(home, "sessions"))
	sink := NewNotificationBuffer()
	analyticsSink := newRecordingTurnEventSink()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent: &apiErrorRuntimeAgent{err: &codexapi.APIError{
			Kind:    codexapi.ErrorInvalidAudioInput,
			Status:  http.StatusBadRequest,
			Message: "invalid audio input: [redacted]",
		}},
		ThreadStatus: NewThreadStatusManager(),
		Analytics:    analyticsSink,
		DefaultCWD:   t.TempDir(),
	})
	router.SetNotificationSink(sink)

	initialize := requestWithParams(t, IntID(1), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex-tui", Version: "1.2.3"},
	})
	initialize.ConnectionID = "conn-invalid-audio-analytics"
	if response := router.Handle(initialize); response.Error != nil {
		t.Fatalf("initialize error: %+v", response.Error)
	}
	threadStart := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{
		CWD: t.TempDir(),
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	turnStart := requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "invalid audio analytics",
	})
	turnStart.ConnectionID = "conn-invalid-audio-analytics"
	response := router.Handle(turnStart)
	if response.Error != nil {
		t.Fatalf("turn start error: %+v", response.Error)
	}
	turnID := response.Result.(*turn.TurnStartResponse).Turn.ID
	completed := waitForTurnCompletedStatus(t, sink, turnID, TurnStatusFailed)
	if completed.Turn.Error == nil {
		t.Fatal("failed turn error missing")
	}
	if completed.Turn.Error.CodexErrorInfo != "badRequest" {
		t.Fatalf("failed turn codexErrorInfo = %#v, want badRequest", completed.Turn.Error.CodexErrorInfo)
	}

	event := waitForCodexTurnEvent(t, analyticsSink, turnID)
	params := event.EventParams
	if params.TurnError != "badRequest" ||
		params.CodexErrorKind == nil || *params.CodexErrorKind != "invalid_audio_input" ||
		params.CodexErrorHTTPStatusCode != nil {
		t.Fatalf("invalid audio analytics error fields = error:%#v kind:%#v http:%#v", params.TurnError, params.CodexErrorKind, params.CodexErrorHTTPStatusCode)
	}
}
