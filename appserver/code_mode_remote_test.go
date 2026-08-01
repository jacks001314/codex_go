package appserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/codemode"
	"codex_go/model"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"

	"github.com/coder/websocket"
)

func TestRuntimeRouterRemoteCodeModeExecutesAndClosesSharedConnection(t *testing.T) {
	connectionClosed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		var hello codemode.ClientToHost
		if !readAppServerCodeModeFrame(t, ctx, conn, &hello) || hello.Type != "connection/hello" {
			return
		}
		writeAppServerCodeModeFrame(t, ctx, conn, codemode.HostHelloMessage(codemode.HostHello{SelectedVersion: codemode.ProtocolV1}))

		var open codemode.ClientToHost
		if !readAppServerCodeModeFrame(t, ctx, conn, &open) || open.Request == nil || open.Request.Method != "session/open" {
			return
		}
		writeAppServerCodeModeFrame(t, ctx, conn, codemode.HostOperationResponse(open.ID, codemode.ResultOK(codemode.SessionReady(open.Request.SessionID))))

		var execute codemode.ClientToHost
		if !readAppServerCodeModeFrame(t, ctx, conn, &execute) || execute.Request == nil || execute.Request.Method != "session/execute" {
			return
		}
		cellID := codemode.CellID("app-server-remote-cell")
		writeAppServerCodeModeFrame(t, ctx, conn, codemode.HostOperationResponse(execute.ID, codemode.ResultOK(codemode.ExecutionStarted(cellID))))
		writeAppServerCodeModeFrame(t, ctx, conn, codemode.InitialResponse(execute.ID, codemode.ResultOK(codemode.Result(cellID, []codemode.ContentItem{codemode.InputText("REMOTE_APP_SERVER_OK")}, nil))))

		var shutdown codemode.ClientToHost
		if readAppServerCodeModeFrame(t, context.Background(), conn, &shutdown) && shutdown.Request != nil && shutdown.Request.Method == "session/shutdown" {
			writeAppServerCodeModeFrame(t, context.Background(), conn, codemode.HostOperationResponse(shutdown.ID, codemode.ResultOK(codemode.SessionClosed(shutdown.Request.SessionID))))
		}
		_, _, _ = conn.Read(context.Background())
		close(connectionClosed)
	}))
	defer server.Close()

	home := t.TempDir()
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{
		CodeModeHostURL:        "ws" + strings.TrimPrefix(server.URL, "http"),
		CodeModeHostHTTPClient: server.Client(),
	})
	toolRouter, err := router.requireToolRouter(home)
	if err != nil {
		t.Fatalf("requireToolRouter() error = %v", err)
	}
	output, err := toolRouter.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "app-server-remote-exec",
		ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload:  tool.Payload{Kind: tool.PayloadCustom, Input: `text("LOCAL_MUST_NOT_RUN")`},
	})
	if err != nil {
		t.Fatalf("remote exec error = %v", err)
	}
	if output == nil || output.Body != "REMOTE_APP_SERVER_OK" {
		t.Fatalf("remote exec output = %#v", output)
	}
	if err := router.Close(); err != nil {
		t.Fatalf("router.Close() error = %v", err)
	}
	select {
	case <-connectionClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("router shutdown did not close the shared code-mode connection")
	}
}

func TestRuntimeRouterRemoteCodeModeNoFallbackReturnsFatalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "host unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	home := t.TempDir()
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{
		CodeModeHostURL:                  "ws" + strings.TrimPrefix(server.URL, "http"),
		CodeModeHostHTTPClient:           server.Client(),
		DisableCodeModeInProcessFallback: true,
	})
	defer router.Close()
	toolRouter, err := router.requireToolRouter(home)
	if err != nil {
		t.Fatalf("requireToolRouter() error = %v", err)
	}
	output, err := toolRouter.Dispatch(context.Background(), &tool.Invocation{
		CallID:   "app-server-no-fallback",
		ToolName: tool.PlainName(tool.CodeModeExecToolName),
		Payload:  tool.Payload{Kind: tool.PayloadCustom, Input: `text("LOCAL_MUST_NOT_RUN")`},
	})
	var callErr *tool.FunctionCallError
	if output != nil || !tool.AsFunctionCallError(err, &callErr) || !callErr.IsFatal() || !strings.Contains(callErr.ModelMessage(), "code-mode remote host unavailable") {
		t.Fatalf("output = %#v error = %v call error = %#v", output, err, callErr)
	}
}

func TestRuntimeRouterSelectsCodeModeProviderFromStartupPolicy(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "codex-code-mode-host-does-not-exist")
	for _, testCase := range []struct {
		name    string
		options *RuntimeRouterOptions
		process bool
	}{
		{name: "disabled", options: &RuntimeRouterOptions{}},
		{name: "feature-enabled", options: &RuntimeRouterOptions{CodeModeHostEnabled: true, CodeModeHostProgram: missing}, process: true},
		{name: "fallback-disabled", options: &RuntimeRouterOptions{DisableCodeModeInProcessFallback: true, CodeModeHostProgram: missing}, process: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router := NewDefaultRuntimeRouterWithOptions(session.NewStore(filepath.Join(home, testCase.name)), filepath.Join(home, testCase.name), testCase.options)
			defer router.Close()
			_, isProcess := router.services.CodeModeProvider.(*codemode.ProcessProvider)
			_, isDisabled := router.services.CodeModeProvider.(*codemode.DisabledProvider)
			if isProcess != testCase.process || isDisabled == testCase.process {
				t.Fatalf("provider = %T", router.services.CodeModeProvider)
			}
			availability := router.services.CodeModeProvider.(tool.CodeModeRemoteAvailabilityProvider).Availability()
			if testCase.process && (availability == nil || !strings.Contains(availability.Error(), missing)) {
				t.Fatalf("process availability = %v", availability)
			}
			if !testCase.process && (availability == nil || availability.Error() != "code-mode host is disabled") {
				t.Fatalf("disabled availability = %v", availability)
			}
		})
	}
}

func TestRuntimeRouterOwnsCodeModeRuntimePerThread(t *testing.T) {
	home := t.TempDir()
	router := NewDefaultRuntimeRouterWithOptions(session.NewStore(home), home, &RuntimeRouterOptions{})
	defer router.Close()
	threadA := router.codeModeRuntimeForThread("thread-a")
	if threadA == nil || router.codeModeRuntimeForThread("thread-a") != threadA {
		t.Fatal("thread-a did not retain its code-mode runtime")
	}
	if threadB := router.codeModeRuntimeForThread("thread-b"); threadB == nil || threadB == threadA {
		t.Fatal("distinct threads shared a code-mode runtime")
	}
	if err := router.deleteCodeModeRuntime("thread-a"); err != nil {
		t.Fatal(err)
	}
	if replacement := router.codeModeRuntimeForThread("thread-a"); replacement == nil || replacement == threadA {
		t.Fatal("unloaded thread reused its closed code-mode runtime")
	}
}

func TestRuntimeRouterCodeModeWarningPrecedesTurnStarted(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "codex-code-mode-host-does-not-exist")
	sink := NewNotificationBuffer()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:     NewRouter(session.NewStore(home)),
		Turns:            turn.NewTurnService(),
		Agent:            model.NewLocalAgentRunner(),
		ThreadStatus:     NewThreadStatusManager(),
		CodeModeProvider: codemode.NewProcessProvider(missing),
	})
	defer router.Close()
	router.SetNotificationSink(sink)

	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: home}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	turnStarted := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{ThreadID: threadID, Prompt: "check warning order"}))
	if turnStarted.Error != nil {
		t.Fatalf("turn/start error = %+v", turnStarted.Error)
	}
	turnID := turnStarted.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)

	warningIndex := -1
	startedIndex := -1
	for index, notification := range sink.List() {
		switch notification.Method {
		case NotificationWarning:
			warning, _ := notification.Params.(*WarningNotification)
			if warning != nil && strings.Contains(warning.Message, missing) {
				warningIndex = index
			}
		case NotificationTurnStarted:
			payload, _ := notification.Params.(*TurnStartedNotification)
			if payload != nil && payload.Turn.ID == turnID {
				startedIndex = index
			}
		}
	}
	if warningIndex < 0 || startedIndex < 0 || warningIndex >= startedIndex {
		t.Fatalf("code-mode warning index = %d, turn/started index = %d, notifications = %#v", warningIndex, startedIndex, sink.List())
	}
}

func readAppServerCodeModeFrame(t *testing.T, ctx context.Context, conn *websocket.Conn, target *codemode.ClientToHost) bool {
	t.Helper()
	messageType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Errorf("Read() error = %v", err)
		return false
	}
	if messageType != websocket.MessageBinary {
		t.Errorf("message type = %v, want binary", messageType)
		return false
	}
	ok, err := codemode.NewFramedReader(bytes.NewReader(payload)).Read(target)
	if err != nil {
		t.Errorf("decode frame error = %v", err)
		return false
	}
	return ok
}

func writeAppServerCodeModeFrame(t *testing.T, ctx context.Context, conn *websocket.Conn, message codemode.HostToClient) {
	t.Helper()
	frame, err := codemode.EncodeFrame(message)
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}
	payload, err := (&frame).Bytes()
	if err != nil {
		t.Fatalf("frame.Bytes() error = %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}
