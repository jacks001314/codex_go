package appserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codex_go/internal/codexapi"
	"codex_go/internal/model"
	"codex_go/internal/session"
	"codex_go/internal/turn"
)

func TestAppServerAttestationHeaderValueWrapsClientToken(t *testing.T) {
	token := "v1.opaque-client-payload"
	value, err := appServerAttestationHeaderValue(appServerAttestationStatusOK, &token)
	if err != nil {
		t.Fatalf("header value error = %v", err)
	}
	if value != `{"v":1,"s":0,"t":"v1.opaque-client-payload"}` {
		t.Fatalf("header value = %q", value)
	}
}

func TestAppServerAttestationHeaderValueReportsFailures(t *testing.T) {
	cases := []struct {
		name   string
		status appServerAttestationStatus
		want   string
	}{
		{"timeout", appServerAttestationStatusTimeout, `{"v":1,"s":1}`},
		{"request failed", appServerAttestationStatusRequestFailed, `{"v":1,"s":2}`},
		{"request canceled", appServerAttestationStatusRequestCanceled, `{"v":1,"s":3}`},
		{"malformed response", appServerAttestationStatusMalformedResponse, `{"v":1,"s":4}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := appServerAttestationHeaderValue(tc.status, nil)
			if err != nil {
				t.Fatalf("header value error = %v", err)
			}
			if value != tc.want {
				t.Fatalf("header value = %q, want %q", value, tc.want)
			}
		})
	}
}

func TestAppServerAttestationProviderRequestsSubscribedOptInConnection(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	initializeAttestationTestConnection(t, router, "conn-b", false)
	initializeAttestationTestConnection(t, router, "conn-a", true)
	router.subscribeThreadConnection("thread-attest", "conn-b")
	router.subscribeThreadConnection("thread-attest", "conn-a")

	selected, ok := router.firstAttestationCapableConnectionForThread("thread-attest")
	if !ok || selected != "conn-a" {
		t.Fatalf("selected connection = %q %v", selected, ok)
	}

	targeted := &attestationTargetSink{router: router}
	router.SetServerRequestSink(targeted)

	value, ok, err := router.appServerAttestationProvider().HeaderForRequest(context.Background(), &codexapi.AttestationContext{ThreadID: "thread-attest"})
	if err != nil {
		t.Fatalf("HeaderForRequest error = %v", err)
	}
	if !ok || value != `{"v":1,"s":0,"t":"v1.client"}` {
		t.Fatalf("header = %q %v", value, ok)
	}
	if len(targeted.requests) != 1 {
		t.Fatalf("requests = %d", len(targeted.requests))
	}
	if targeted.connectionID != "conn-a" {
		t.Fatalf("target connection = %q", targeted.connectionID)
	}
}

func TestAppServerAttestationProviderSkipsWithoutSubscribedOptInConnection(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	initializeAttestationTestConnection(t, router, "conn-a", false)
	router.subscribeThreadConnection("thread-attest", "conn-a")
	requests := 0
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		requests++
	}))

	value, ok, err := router.appServerAttestationProvider().HeaderForRequest(context.Background(), &codexapi.AttestationContext{ThreadID: "thread-attest"})
	if err != nil {
		t.Fatalf("HeaderForRequest error = %v", err)
	}
	if ok || value != "" {
		t.Fatalf("header = %q %v", value, ok)
	}
	if requests != 0 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestAppServerAttestationProviderMapsServerRequestFailure(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	initializeAttestationTestConnection(t, router, "conn-a", true)
	router.subscribeThreadConnection("thread-attest", "conn-a")

	value, ok, err := router.appServerAttestationProvider().HeaderForRequest(context.Background(), &codexapi.AttestationContext{ThreadID: "thread-attest"})
	if err != nil {
		t.Fatalf("HeaderForRequest error = %v", err)
	}
	if !ok || value != `{"v":1,"s":2}` {
		t.Fatalf("header = %q %v", value, ok)
	}
}

func TestRuntimeRouterTurnStartSendsAppServerAttestationHeader(t *testing.T) {
	attestation := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case attestation <- r.Header.Get(codexapi.AttestationHeader):
		default:
		}
		_, _ = w.Write([]byte(`{"id":"resp-attest","model":"gpt-test","output_text":"ok","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	store := session.NewStore(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Agent: model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
			Provider:           &model.APIProvider{BaseURL: server.URL + "/v1"},
			IncludeAttestation: true,
		}),
	})
	connectionID := "conn-attest"
	initializeAttestationTestConnection(t, router, connectionID, true)
	router.SetServerRequestSink(ServerRequestSinkFunc(func(request *ServerRequest) {
		if request.Method != ServerRequestAttestationGenerate {
			t.Errorf("server request method = %q", request.Method)
			return
		}
		router.resolveServerResponse(OK(request.ID, &AttestationGenerateResponse{Token: "v1.client"}))
	}))

	startRequest := requestWithParams(t, StringID("thread-start-attest"), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()})
	startRequest.ConnectionID = connectionID
	start := router.Handle(startRequest)
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	turnRequest := requestWithParams(t, StringID("turn-start-attest"), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "hello",
	})
	turnRequest.ConnectionID = connectionID
	response := router.Handle(turnRequest)
	if response.Error != nil {
		t.Fatalf("turn/start error: %+v", response.Error)
	}

	select {
	case got := <-attestation:
		if got != `{"v":1,"s":0,"t":"v1.client"}` {
			t.Fatalf("attestation header = %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for responses request")
	}
}

func initializeAttestationTestConnection(t *testing.T, router *RuntimeRouter, connectionID string, requestAttestation bool) {
	t.Helper()
	request := requestWithParams(t, StringID("init-"+connectionID), MethodInitialize, InitializeParams{
		ClientInfo: ClientInfo{Name: "codex_vscode", Version: "0.1.0"},
		Capabilities: &InitializeCapabilities{
			RequestAttestation: requestAttestation,
		},
	})
	request.ConnectionID = connectionID
	response := router.Handle(request)
	if response.Error != nil {
		t.Fatalf("initialize %s error: %+v", connectionID, response.Error)
	}
}

type attestationTargetSink struct {
	router       *RuntimeRouter
	connectionID string
	requests     []*ServerRequest
}

func (s *attestationTargetSink) SendServerRequest(request *ServerRequest) {
	s.SendServerRequestToConnection("", request)
}

func (s *attestationTargetSink) SendServerRequestToConnection(connectionID string, request *ServerRequest) {
	s.connectionID = connectionID
	s.requests = append(s.requests, request)
	if request.Method != ServerRequestAttestationGenerate {
		return
	}
	s.router.resolveServerResponse(OK(request.ID, &AttestationGenerateResponse{Token: "v1.client"}))
}
