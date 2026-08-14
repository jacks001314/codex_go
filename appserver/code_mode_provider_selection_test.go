package appserver

import (
	"testing"

	"codex_go/codemode"
	"codex_go/session"
)

func TestDefaultRuntimeRouterSelectsCodeModeProviderByURL(t *testing.T) {
	store := session.NewStore(t.TempDir())
	grpc := NewDefaultRuntimeRouterWithOptions(store, t.TempDir(), &RuntimeRouterOptions{CodeModeHostURL: "http://127.0.0.1:8765"})
	if _, ok := grpc.services.CodeModeProvider.(*codemode.GrpcCodeModeSessionProvider); !ok {
		t.Fatalf("http code-mode host provider = %T, want gRPC provider", grpc.services.CodeModeProvider)
	}

	ws := NewDefaultRuntimeRouterWithOptions(store, t.TempDir(), &RuntimeRouterOptions{CodeModeHostURL: "ws://127.0.0.1:8765"})
	if _, ok := ws.services.CodeModeProvider.(*codemode.WebSocketProvider); !ok {
		t.Fatalf("ws code-mode host provider = %T, want WebSocket provider", ws.services.CodeModeProvider)
	}
}
