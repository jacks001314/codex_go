package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"codex_go/auth"
	"codex_go/realtime"
	codextea "codex_go/tui/tea"
	"codex_go/voicehost"
)

// Mirrors Rust #61172: the TUI creates the WebRTC call with its own provider
// and credentials, then attaches the app-server through existingCall.
func TestRemoteVoiceOwnedCallCreatesClientCallLikeRust(t *testing.T) {
	type capturedCall struct {
		path          string
		authorization string
		sessionID     string
		threadID      string
		contentType   string
		body          string
	}
	var (
		mu   sync.Mutex
		seen capturedCall
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		mu.Lock()
		seen = capturedCall{
			path:          request.URL.Path,
			authorization: request.Header.Get("Authorization"),
			sessionID:     request.Header.Get("session-id"),
			threadID:      request.Header.Get("thread-id"),
			contentType:   request.Header.Get("Content-Type"),
			body:          string(body),
		}
		mu.Unlock()
		writer.Header().Set("Location", "/v1/live/rtc_client_owned")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("v=0 answer"))
	}))
	defer server.Close()

	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(strings.Join([]string{
		`openai_base_url = "` + server.URL + `/v1"`,
		`experimental_realtime_ws_backend_prompt = "server prompt"`,
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := auth.NewStoreWithOptions(home, auth.StoreOptionsFromConfig("file", false)).Save(auth.AuthDotJSON{
		OpenAIAPIKey: "sk-test",
	}); err != nil {
		t.Fatal(err)
	}

	params, answer, err := negotiateVoiceOwnedCall(context.Background(), home, func() string { return home }, nil,
		"thread-voice", "v=0 offer", VoiceSettings{Model: "gpt-live-1.1-codex", Voice: "marin", VoiceSet: true})
	if err != nil {
		t.Fatalf("negotiateVoiceOwnedCall() error = %v", err)
	}
	if answer != "v=0 answer" {
		t.Fatalf("answer = %q", answer)
	}
	if params.Transport == nil || params.Transport.Type != "existingCall" || params.Transport.CallID != "rtc_client_owned" {
		t.Fatalf("transport = %#v", params.Transport)
	}
	if params.Version == nil || *params.Version != realtime.VersionV3 {
		t.Fatalf("version = %#v", params.Version)
	}
	if params.IncludeStartupContext == nil || *params.IncludeStartupContext {
		t.Fatalf("includeStartupContext = %#v", params.IncludeStartupContext)
	}
	if params.ClientManagedHandoffs == nil || !*params.ClientManagedHandoffs {
		t.Fatalf("clientManagedHandoffs = %#v", params.ClientManagedHandoffs)
	}
	if params.Model != nil || params.Voice != nil {
		t.Fatalf("existingCall start carried session configuration: model=%#v voice=%#v", params.Model, params.Voice)
	}
	if params.RealtimeSessionID == nil || *params.RealtimeSessionID != "thread-voice" {
		t.Fatalf("realtimeSessionId = %#v", params.RealtimeSessionID)
	}

	mu.Lock()
	call := seen
	mu.Unlock()
	if call.path != "/v1/live" {
		t.Fatalf("call path = %q, want the v3 live route", call.path)
	}
	if call.authorization != "Bearer sk-test" {
		t.Fatalf("authorization = %q", call.authorization)
	}
	if call.sessionID != "thread-voice" || call.threadID != "thread-voice" {
		t.Fatalf("thread headers = session %q thread %q", call.sessionID, call.threadID)
	}
	if !strings.Contains(call.contentType, "multipart/form-data") {
		t.Fatalf("content type = %q", call.contentType)
	}
	if !strings.Contains(call.body, "v=0 offer") || !strings.Contains(call.body, "server prompt") ||
		!strings.Contains(call.body, "gpt-live-1.1-codex") || !strings.Contains(call.body, "marin") {
		t.Fatalf("call body lost its session configuration:\n%s", call.body)
	}
}

// A missing credential fails the attempt before any call is created.
func TestRemoteVoiceOwnedCallRequiresCredentialsLikeRust(t *testing.T) {
	home := t.TempDir()
	_, _, err := negotiateVoiceOwnedCall(context.Background(), home, nil, nil, "thread-voice", "v=0 offer", VoiceSettings{})
	if err == nil || !strings.Contains(err.Error(), "signed-in account or API key") {
		t.Fatalf("error = %v", err)
	}
}

// Mirrors the Rust start ordering: the negotiated answer reaches the helper
// before the app-server attaches, and a refused attachment retires the helper.
func TestVoiceRuntimeAppliesOwnedCallAnswerBeforeAttachLikeRust(t *testing.T) {
	previousApply := applyVoiceAnswerSDP
	var applied []string
	applyVoiceAnswerSDP = func(_ *voicehost.SessionHandle, answer string) error {
		applied = append(applied, answer)
		return nil
	}
	t.Cleanup(func() { applyVoiceAnswerSDP = previousApply })
	withFakeVoiceSession(t, &voicehost.StartedSession{
		OfferSDP: "v=0 offer",
		Handle:   &voicehost.SessionHandle{},
	}, nil)

	var steps []string
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		ownCall: func(context.Context, string, string, VoiceSettings) (realtime.StartParams, string, error) {
			steps = append(steps, "create")
			return realtime.StartParams{
				ThreadID:  "thread-owned",
				Transport: realtime.ExistingCallTransport("rtc_owned"),
			}, "v=0 answer", nil
		},
		startSession: func(_ context.Context, params realtime.StartParams) error {
			steps = append(steps, "attach:"+params.Transport.CallID)
			return nil
		},
	})
	msg := runtime.startCmd("thread-owned", 9)()
	if attached, ok := msg.(codextea.VoiceHelperAttachedMsg); !ok || attached.AttemptID != 9 {
		t.Fatalf("start reported %#v", msg)
	}
	if len(applied) != 1 || applied[0] != "v=0 answer" {
		t.Fatalf("answers = %#v", applied)
	}
	if len(steps) != 2 || steps[0] != "create" || steps[1] != "attach:rtc_owned" {
		t.Fatalf("steps = %#v", steps)
	}
}

// Mirrors Rust #61172: a stop while call negotiation is in flight retires the
// helper and never sends the attach request for the retired attempt.
func TestVoiceRuntimeDropsOwnedCallSupersededByStopLikeRust(t *testing.T) {
	withFakeVoiceSession(t, &voicehost.StartedSession{
		OfferSDP: "v=0 offer",
		Handle:   &voicehost.SessionHandle{},
	}, nil)
	negotiating := make(chan struct{})
	release := make(chan struct{})
	attached := 0
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		ownCall: func(context.Context, string, string, VoiceSettings) (realtime.StartParams, string, error) {
			close(negotiating)
			<-release
			return realtime.StartParams{ThreadID: "thread-stop", Transport: realtime.ExistingCallTransport("rtc_late")}, "v=0 answer", nil
		},
		startSession: func(context.Context, realtime.StartParams) error {
			attached++
			return nil
		},
	})
	cmd := runtime.startCmd("thread-stop", 12)
	result := make(chan any, 1)
	go func() { result <- cmd() }()
	<-negotiating
	if err := runtime.close(true); err != nil {
		t.Fatalf("close: %v", err)
	}
	close(release)
	msg := <-result
	if _, ok := msg.(codextea.VoiceHelperAttachedMsg); ok {
		t.Fatalf("a superseded attempt attached: %#v", msg)
	}
	if attached != 0 {
		t.Fatalf("attach requests = %d, want none for a retired attempt", attached)
	}
	if runtime.running() {
		t.Fatal("a superseded attempt retained the helper")
	}
}

// Mirrors Rust #61172: a restarted attempt wins over an earlier negotiation
// whose result is delivered afterwards.
func TestVoiceRuntimeKeepsOnlyNewestOwnedCallLikeRust(t *testing.T) {
	previousApply := applyVoiceAnswerSDP
	applyVoiceAnswerSDP = func(*voicehost.SessionHandle, string) error { return nil }
	t.Cleanup(func() { applyVoiceAnswerSDP = previousApply })
	withFakeVoiceSession(t, &voicehost.StartedSession{
		OfferSDP: "v=0 offer",
		Handle:   &voicehost.SessionHandle{},
	}, nil)
	firstNegotiating := make(chan struct{})
	releaseFirst := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	runtime := newVoiceRuntime(voiceRuntimeOptions{
		ownCall: func(context.Context, string, string, VoiceSettings) (realtime.StartParams, string, error) {
			mu.Lock()
			calls++
			current := calls
			mu.Unlock()
			if current == 1 {
				close(firstNegotiating)
				<-releaseFirst
				return realtime.StartParams{ThreadID: "thread-newest", Transport: realtime.ExistingCallTransport("rtc_old")}, "v=0 answer", nil
			}
			return realtime.StartParams{ThreadID: "thread-newest", Transport: realtime.ExistingCallTransport("rtc_new")}, "v=0 answer", nil
		},
		startSession: func(context.Context, realtime.StartParams) error { return nil },
	})
	older := runtime.startCmd("thread-newest", 20)
	olderResult := make(chan any, 1)
	go func() { olderResult <- older() }()
	<-firstNegotiating
	if msg := runtime.startCmd("thread-newest", 21)(); msg == nil {
		t.Fatal("the restarted attempt reported nothing")
	}
	close(releaseFirst)
	if msg := <-olderResult; msg != nil {
		if _, ok := msg.(codextea.VoiceHelperAttachedMsg); ok {
			t.Fatalf("the earlier attempt replaced the newest one: %#v", msg)
		}
	}
	session := runtime.currentSession()
	if session == nil {
		t.Fatal("the newest attempt was not attached")
	}
}
