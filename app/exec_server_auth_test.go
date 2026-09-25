package app

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	codexexecserver "codex_go/execserver"
)

// execServerURLPipe captures the `ws://host:port` line the exec-server prints
// when it starts listening.
type execServerURLPipe struct {
	mu  sync.Mutex
	buf bytes.Buffer
	url chan string
}

func (p *execServerURLPipe) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, _ = p.buf.Write(data)
	text := strings.TrimSpace(p.buf.String())
	if index := strings.Index(text, "ws://"); index >= 0 {
		candidate := strings.TrimSpace(text[index:])
		if newline := strings.IndexAny(candidate, "\r\n"); newline >= 0 {
			candidate = candidate[:newline]
		}
		select {
		case p.url <- candidate:
		default:
		}
	}
	return len(data), nil
}

// Rust parity: codex-rs/cli/tests/exec_server_websocket_auth.rs (#47601): the
// `codex exec-server` listener rejects unauthenticated upgrades with 401 and
// accepts the configured capability token.
func TestRunExecServerListenerWebSocketAuthLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	const token = "capability-token-value"
	tokenFile := filepath.Join(t.TempDir(), "token.txt")
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout := &execServerURLPipe{url: make(chan string, 1)}
	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(ctx, []string{
			"exec-server",
			"--listen", "ws://127.0.0.1:0",
			"--ws-auth", "capability-token",
			"--ws-token-file", tokenFile,
		}, strings.NewReader(""), stdout, io.Discard)
	}()

	var serverURL string
	select {
	case serverURL = <-stdout.url:
	case err := <-runDone:
		t.Fatalf("exec-server exited before listening: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("exec-server did not report its listen URL")
	}

	if _, err := codexexecserver.DialClient(context.Background(), serverURL, "app-exec-server-auth-test"); err == nil {
		t.Fatal("unauthenticated dial succeeded against an authenticated exec-server listener")
	} else if !strings.Contains(err.Error(), "401") {
		t.Fatalf("unauthenticated dial error = %v, want HTTP 401", err)
	}

	client, err := codexexecserver.DialClientWithOptions(context.Background(), serverURL, codexexecserver.DialClientOptions{
		ClientName:  "app-exec-server-auth-test",
		HTTPHeaders: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		t.Fatalf("authenticated dial error = %v", err)
	}
	if client.SessionID() == "" {
		t.Fatal("authenticated client session id is empty")
	}
	_ = client.Close()

	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("exec-server did not stop after cancellation")
	}
}
