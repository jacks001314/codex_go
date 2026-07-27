package appserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codex_go/codemode"
	"codex_go/session"
	"codex_go/tool"

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
