package app

import (
	"context"
	"errors"
	"testing"
	"time"

	codextea "codex_go/tui/tea"
)

type queuedRemoteTUITransport struct {
	messages [][]byte
}

func (t *queuedRemoteTUITransport) read(context.Context) ([]byte, error) {
	if len(t.messages) == 0 {
		return nil, errors.New("transport closed")
	}
	message := t.messages[0]
	t.messages = t.messages[1:]
	return message, nil
}

func (*queuedRemoteTUITransport) write(context.Context, []byte) error { return nil }
func (*queuedRemoteTUITransport) close()                              {}

// TestRemoteMCPLoginMonitorWaitsForMatchingAttemptLikeRust pins the remote
// half of #49290: stale login ids for the same server are ignored and only the
// requested attempt reaches the TUI completion channel.
func TestRemoteMCPLoginMonitorWaitsForMatchingAttemptLikeRust(t *testing.T) {
	transport := &queuedRemoteTUITransport{messages: [][]byte{
		[]byte(`{"method":"mcpServer/oauthLogin/completed","params":{"name":"enterprise","threadId":"thread-1","loginId":"old","success":false,"error":"Enterprise sign-in failed."}}`),
		[]byte(`{"method":"mcpServer/oauthLogin/completed","params":{"name":"enterprise","threadId":"thread-1","loginId":"new","success":true}}`),
	}}
	client := &remoteAppServerTUIClient{transport: transport}
	completions := make(chan codextea.MCPLoginCompletedMsg, 1)
	go monitorRemoteMCPLoginCompletion(context.Background(), client, "enterprise", "new", completions)

	select {
	case completion := <-completions:
		if completion.LoginID != "new" || !completion.Success || completion.ThreadID != "thread-1" {
			t.Fatalf("completion = %#v", completion)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for matching remote login completion")
	}
}
