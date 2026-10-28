package codemode

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/tool"

	"github.com/coder/websocket"
	"google.golang.org/grpc"
)

// Mirrors Rust #57615: the code-mode host allocates cell generations across
// sessions, so a reloaded thread's replacement session cannot reuse cell IDs
// from its previous session.
func TestWebSocketProviderSharesCellGenerationsAcrossSessionsLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(w, request, nil)
		if err != nil {
			t.Errorf("Accept() error = %v", err)
			return
		}
		defer conn.CloseNow()
		ctx := request.Context()
		var hello ClientToHost
		if !readClientFrame(t, ctx, conn, &hello) || hello.Type != "connection/hello" {
			return
		}
		writeHostFrame(t, ctx, conn, HostHelloMessage(HostHello{SelectedVersion: ProtocolV1, Capabilities: CapabilitySet{}}))
		for {
			var message ClientToHost
			if !readClientFrame(t, ctx, conn, &message) || message.Request == nil {
				return
			}
			switch message.Request.Method {
			case "session/open":
				writeHostFrame(t, ctx, conn, HostOperationResponse(message.ID, ResultOK(SessionReady(message.Request.SessionID))))
			case "session/execute":
				// The host restarts cell numbering per session; the client-side
				// generation keeps the public IDs distinct.
				cellID := CellID("cell-1")
				writeHostFrame(t, ctx, conn, HostOperationResponse(message.ID, ResultOK(ExecutionStarted(cellID))))
				writeHostFrame(t, ctx, conn, InitialResponse(message.ID, ResultOK(Result(cellID, []ContentItem{InputText("OK")}, nil))))
			case "session/shutdown":
				writeHostFrame(t, ctx, conn, HostOperationResponse(message.ID, ResultOK(SessionClosed(message.Request.SessionID))))
			default:
				return
			}
		}
	}))
	defer server.Close()

	provider := NewWebSocketProvider("ws"+strings.TrimPrefix(server.URL, "http"), server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	first := provider.NewSession(&recordingRemoteDelegate{})
	response, err := first.Execute(ctx, tool.CodeModeRemoteExecuteRequest{ToolCallID: "exec-1", Source: `text("OK")`}, nil)
	if err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if response.CellID != "cell-1" {
		t.Fatalf("first cell id = %q, want cell-1", response.CellID)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	// A replacement session after a thread reload must not reuse the previous
	// session's cell namespace.
	second := provider.NewSession(&recordingRemoteDelegate{})
	response, err = second.Execute(ctx, tool.CodeModeRemoteExecuteRequest{ToolCallID: "exec-2", Source: `text("OK")`}, nil)
	if err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if response.CellID != "g2:cell-1" {
		t.Fatalf("second cell id = %q, want g2:cell-1", response.CellID)
	}
	_ = second.Close()
	_ = provider.Close()
}

// Mirrors Rust #57615's gRPC host coverage: generations are allocated by the
// gRPC provider, so the replacement session's first cell is not reported with
// the previous session's ID.
func TestGrpcCodeModeProviderSharesCellGenerationsAcrossSessionsLikeRust(t *testing.T) {
	server := grpc.NewServer()
	codemodev1.RegisterCodeModeHostServer(server, &grpcSessionHostServer{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	provider := NewGrpcCodeModeSessionProvider("http://"+listener.Addr().String(), &http.Client{Transport: http.DefaultTransport})
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first := provider.NewSession(&recordingGrpcDelegate{})
	response, err := first.Execute(ctx, tool.CodeModeRemoteExecuteRequest{ToolCallID: "call-1", Source: "test"}, nil)
	if err != nil {
		t.Fatalf("first Execute() error = %v", err)
	}
	if response.CellID != "cell-1" {
		t.Fatalf("first cell id = %q, want cell-1", response.CellID)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second := provider.NewSession(&recordingGrpcDelegate{})
	response, err = second.Execute(ctx, tool.CodeModeRemoteExecuteRequest{ToolCallID: "call-2", Source: "test"}, nil)
	if err != nil {
		t.Fatalf("second Execute() error = %v", err)
	}
	if response.CellID != "g2:cell-1" {
		t.Fatalf("second cell id = %q, want g2:cell-1", response.CellID)
	}
	_ = second.Close()
}
