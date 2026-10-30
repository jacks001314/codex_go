package appserver

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/remotecontrol"
)

type stdioTestBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *stdioTestBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *stdioTestBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type blockingEnrollDoer struct {
	entered chan struct{}
	once    sync.Once
}

func (d *blockingEnrollDoer) Do(request *http.Request) (*http.Response, error) {
	d.once.Do(func() { close(d.entered) })
	<-request.Context().Done()
	return nil, request.Context().Err()
}

// TestStdioShutdownCancelsPendingRemoteControlEnrollmentLikeRust covers Rust
// #42668: stdio EOF must interrupt a blocked remote-control enrollment before
// in-flight RPCs drain, so the app server (and its thread writers) can exit.
func TestStdioShutdownCancelsPendingRemoteControlEnrollmentLikeRust(t *testing.T) {
	store, err := remotecontrol.OpenEnrollmentStore(filepath.Join(t.TempDir(), "state_5.sqlite"))
	if err != nil {
		t.Fatalf("OpenEnrollmentStore() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema() error = %v", err)
	}
	target, err := remotecontrol.NormalizeRemoteControlURL("https://chatgpt.com/remote/control")
	if err != nil {
		t.Fatalf("NormalizeRemoteControlURL() error = %v", err)
	}
	doer := &blockingEnrollDoer{entered: make(chan struct{})}
	manager := remotecontrol.NewManager("stdio-shutdown-test", "installation-id")
	manager.ConfigureBackend(&remotecontrol.ManagerBackendOptions{
		Target: target,
		Store:  store,
		AuthLoader: func(context.Context) (*remotecontrol.RemoteControlConnectionAuth, error) {
			return &remotecontrol.RemoteControlConnectionAuth{AccountID: "account-a"}, nil
		},
		ServerAPIOptions: &remotecontrol.ServerAPIOptions{HTTPClient: doer},
	})
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir(), Remote: manager})

	stdinReader, stdinWriter := io.Pipe()
	stdout := &stdioTestBuffer{}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serveJSONLineConnectionContext(router, context.Background(), stdinReader, stdout, nil)
	}()
	go func() {
		_, _ = stdinWriter.Write([]byte(
			"{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\",\"params\":{\"clientInfo\":{\"name\":\"shutdown-test\",\"version\":\"1.0.0\"},\"capabilities\":{\"experimentalApi\":true}}}\n" +
				"{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"remoteControl/enable\",\"params\":{}}\n",
		))
	}()

	select {
	case <-doer.entered:
	case <-time.After(10 * time.Second):
		t.Fatalf("remote control enrollment did not start; stdio output = %q", stdout.String())
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatalf("close stdin error = %v", err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve error = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stdio drain waited for remote control enrollment after shutdown")
	}
	if got := stdout.String(); !strings.Contains(got, remotecontrol.ErrRemoteControlShuttingDown.Error()) {
		t.Fatalf("stdio response = %q, want the remote control shutdown error", got)
	}
}
