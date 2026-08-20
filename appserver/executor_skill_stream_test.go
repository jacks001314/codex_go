package appserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/execserver"
	execserverclient "codex_go/execserver"
)

func TestReadClientEnvironmentTextStreamReturnsContents(t *testing.T) {
	// Rust #39620: executor skill resources stream through fs/open +
	// fs/readBlock with incremental size limits.
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	body := "---\nname: demo\n---\n\nROOT_QUALIFIED_EXECUTOR_SKILL\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	server, client := startExecServerTestClient(t)
	defer server.Close()
	defer client.Close()

	contents, err := readClientEnvironmentTextStream(context.Background(), client, path, int64(len(body)))
	if err != nil {
		t.Fatalf("readClientEnvironmentTextStream() error = %v", err)
	}
	if !strings.Contains(contents, "ROOT_QUALIFIED_EXECUTOR_SKILL") {
		t.Fatalf("contents = %q", contents)
	}
}

func TestReadClientEnvironmentTextStreamRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxExecutorSkillResourceBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	server, client := startExecServerTestClient(t)
	defer server.Close()
	defer client.Close()

	if _, err := readClientEnvironmentTextStream(context.Background(), client, path, int64(maxExecutorSkillResourceBytes+1)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized read error = %v", err)
	}
}

func startExecServerTestClient(t *testing.T) (interface{ Close() error }, *execserverclient.Client) {
	t.Helper()
	serverCtx, cancel := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	server := &testExecServerHandle{ctx: serverCtx, cancel: cancel, done: serverDone}
	go func() {
		serverDone <- execserver.NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &appServerExecServerURLWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("exec-server URL was not reported")
	}
	client, err := execserverclient.DialClient(context.Background(), serverURL, "executor-skill-stream-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	return server, client
}

type testExecServerHandle struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan error
}

func (h *testExecServerHandle) Close() error {
	h.cancel()
	<-h.done
	return nil
}
