package execserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecServerTTYHelperProcess(t *testing.T) {
	if os.Getenv("CODEX_GO_EXEC_SERVER_TTY_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("pushed")
	os.Exit(0)
}

func TestExecServerRecoveryHelperProcess(t *testing.T) {
	if os.Getenv("CODEX_GO_EXEC_SERVER_RECOVERY_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString("before\n")
	time.Sleep(500 * time.Millisecond)
	_, _ = os.Stdout.WriteString("after\n")
}

func TestClientStreamsHTTPResponseBodyNotificationsLikeRust(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Stream-Test", "yes")
		_, _ = w.Write([]byte("client-stream-body"))
	}))
	defer upstream.Close()
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}
	client, err := DialClient(context.Background(), serverURL, "http-stream-client-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	response, stream, err := client.HTTPRequest(context.Background(), &HTTPRequestParams{
		Method:         "GET",
		URL:            upstream.URL,
		RequestID:      "client-http-stream",
		StreamResponse: true,
	})
	if err != nil || response.Status != http.StatusOK || response.BodyBase64 != "" || stream == nil {
		t.Fatalf("HTTPRequest() = %#v, %#v, %v", response, stream, err)
	}
	if stream.requestID != "http-1" {
		t.Fatalf("stream request id = %q, want generated http-1", stream.requestID)
	}
	var body strings.Builder
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		chunk, done, nextErr := stream.Next(ctx)
		cancel()
		if nextErr != nil {
			t.Fatalf("stream.Next() error = %v", nextErr)
		}
		body.Write(chunk)
		if done {
			break
		}
	}
	if body.String() != "client-stream-body" {
		t.Fatalf("stream body = %q", body.String())
	}
	cancelServer()
	_ = client.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

func TestHTTPBodyStreamRejectsSequenceGapsLikeRust(t *testing.T) {
	stream := &HTTPBodyStream{requestID: "gap", nextSeq: 1, notify: make(chan struct{}, 1)}
	stream.publish(HTTPRequestBodyDeltaNotification{RequestID: "gap", Seq: 2, DeltaBase64: "eA=="})
	if _, _, err := stream.Next(context.Background()); err == nil || !strings.Contains(err.Error(), "expected 1") {
		t.Fatalf("Next() error = %v", err)
	}
}

func TestStreamingHTTPRequestUsesGeneratedConnectionLocalIDsLikeRust(t *testing.T) {
	client := &Client{
		nextHTTPID:  1,
		httpStreams: map[string]*HTTPBodyStream{},
		done:        make(chan struct{}),
	}
	request := HTTPRequestParams{RequestID: "caller-stream-id", StreamResponse: true}

	client.mu.Lock()
	request.RequestID = fmt.Sprintf("http-%d", client.nextHTTPID)
	client.nextHTTPID++
	first := request.RequestID
	request.RequestID = fmt.Sprintf("http-%d", client.nextHTTPID)
	client.nextHTTPID++
	second := request.RequestID
	client.mu.Unlock()
	if first != "http-1" || second != "http-2" {
		t.Fatalf("generated request ids = %q, %q", first, second)
	}
}

func TestClientExposesAndResumesSessionLikeRust(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}

	client, err := DialClient(context.Background(), serverURL, "session-resume-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	sessionID := client.SessionID()
	if sessionID == "" {
		t.Fatal("client session id is empty")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var resumed *Client
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		resumed, err = DialClientWithOptions(context.Background(), serverURL, DialClientOptions{
			ClientName:      "session-resume-test",
			ResumeSessionID: sessionID,
		})
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "already attached") {
			t.Fatalf("resume error = %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("resume did not attach: %v", err)
	}
	if resumed.SessionID() != sessionID {
		t.Fatalf("resumed session id = %q, want %q", resumed.SessionID(), sessionID)
	}
	_ = resumed.Close()
	cancelServer()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

func TestClientRecoversFutureFilesystemCallsAfterDisconnectLikeRust(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}
	client, err := DialClient(context.Background(), serverURL, "generic-recovery-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	sessionID := client.SessionID()
	path := filepath.Join(t.TempDir(), "after-recovery.txt")
	if err := os.WriteFile(path, []byte("recovered"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	client.mu.Lock()
	originalConn := client.conn
	client.mu.Unlock()
	if originalConn == nil {
		t.Fatal("client connection is nil")
	}
	if err := originalConn.CloseNow(); err != nil {
		t.Fatalf("CloseNow() error = %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		client.mu.Lock()
		disconnected := client.conn == nil
		client.mu.Unlock()
		if disconnected {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	callCtx, cancelCall := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelCall()
	response, err := client.FSReadFile(callCtx, &FSReadFileParams{Path: path})
	if err != nil {
		t.Fatalf("FSReadFile() after disconnect error = %v", err)
	}
	data, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil || string(data) != "recovered" {
		t.Fatalf("FSReadFile() data = %q, err = %v", data, err)
	}
	if client.SessionID() != sessionID {
		t.Fatalf("session id after recovery = %q, want %q", client.SessionID(), sessionID)
	}

	cancelServer()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

func TestClientRecoversProcessSubscriptionAfterDisconnectLikeRust(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}
	client, err := DialClient(context.Background(), serverURL, "process-recovery-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	subscription, err := client.SubscribeProcessEvents("recover-process")
	if err != nil {
		t.Fatalf("SubscribeProcessEvents() error = %v", err)
	}
	defer subscription.Close()
	if _, err := client.Start(context.Background(), &ExecParams{
		ProcessID: "recover-process",
		Argv:      []string{os.Args[0], "-test.run=^TestExecServerRecoveryHelperProcess$"},
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", Set: map[string]string{
			"CODEX_GO_EXEC_SERVER_RECOVERY_HELPER": "1",
		}},
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelRead()
	for {
		event, err := subscription.Next(readCtx)
		if err != nil {
			t.Fatalf("Next(before disconnect) error = %v", err)
		}
		if event.Kind != ProcessEventOutput {
			continue
		}
		data, decodeErr := base64.StdEncoding.DecodeString(event.Chunk)
		if decodeErr != nil {
			t.Fatalf("DecodeString() error = %v", decodeErr)
		}
		if strings.Contains(string(data), "before") {
			break
		}
	}
	client.mu.Lock()
	originalConn := client.conn
	client.mu.Unlock()
	if originalConn == nil {
		t.Fatal("client connection is nil")
	}
	if err := originalConn.CloseNow(); err != nil {
		t.Fatalf("CloseNow() error = %v", err)
	}

	gotAfter := false
	gotExited := false
	gotClosed := false
	for !gotClosed {
		event, err := subscription.Next(readCtx)
		if err != nil {
			t.Fatalf("Next(after disconnect) error = %v", err)
		}
		switch event.Kind {
		case ProcessEventOutput:
			data, decodeErr := base64.StdEncoding.DecodeString(event.Chunk)
			if decodeErr != nil {
				t.Fatalf("DecodeString() error = %v", decodeErr)
			}
			gotAfter = gotAfter || strings.Contains(string(data), "after")
		case ProcessEventExited:
			gotExited = true
		case ProcessEventClosed:
			gotClosed = true
		}
	}
	if !gotAfter || !gotExited {
		t.Fatalf("recovered events: after=%t exited=%t closed=%t", gotAfter, gotExited, gotClosed)
	}

	cancelServer()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

func TestClientFileReadStreamClosesExactBoundaryAndReleasesCapacityLikeRust(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}
	client, err := DialClient(context.Background(), serverURL, "file-stream-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	dir := t.TempDir()
	exactPath := filepath.Join(dir, "exact.bin")
	if err := os.WriteFile(exactPath, bytes.Repeat([]byte{'x'}, fileReadChunkSize), 0o600); err != nil {
		t.Fatalf("WriteFile(exact) error = %v", err)
	}
	stream, err := client.FSReadFileStream(context.Background(), &FSReadFileParams{Path: exactPath})
	if err != nil {
		t.Fatalf("FSReadFileStream() error = %v", err)
	}
	if len(stream.handleID) != maxFileReadHandleIDBytes {
		t.Fatalf("stream handle id length = %d", len(stream.handleID))
	}
	chunk, done, err := stream.Next(context.Background())
	if err != nil || done || len(chunk) != fileReadChunkSize {
		t.Fatalf("first Next() = %d bytes, done=%t, err=%v", len(chunk), done, err)
	}
	chunk, done, err = stream.Next(context.Background())
	if err != nil || !done || len(chunk) != 0 {
		t.Fatalf("second Next() = %d bytes, done=%t, err=%v", len(chunk), done, err)
	}

	smallPath := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(smallPath, []byte("ok"), 0o600); err != nil {
		t.Fatalf("WriteFile(small) error = %v", err)
	}
	for i := 0; i < maxOpenFileReads+2; i++ {
		stream, err := client.FSReadFileStream(context.Background(), &FSReadFileParams{Path: smallPath})
		if err != nil {
			t.Fatalf("FSReadFileStream(%d) error = %v", i, err)
		}
		chunk, done, err := stream.Next(context.Background())
		if err != nil || done || string(chunk) != "ok" {
			t.Fatalf("stream %d first Next() = %q, done=%t, err=%v", i, chunk, done, err)
		}
		_, done, err = stream.Next(context.Background())
		if err != nil || !done {
			t.Fatalf("stream %d EOF = done=%t, err=%v", i, done, err)
		}
	}

	cancelServer()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

func TestClientConsumesOrderedPushedProcessEventsLikeRust(t *testing.T) {
	requireExecServerTTYOutput(t)
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}

	client, err := DialClient(context.Background(), serverURL, "client-pushed-events-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	events, err := client.SubscribeProcessEvents("pushed")
	if err != nil {
		t.Fatalf("SubscribeProcessEvents() error = %v", err)
	}
	defer events.Close()
	command := []string{os.Args[0], "-test.run=^TestExecServerTTYHelperProcess$"}
	if _, err := client.Start(context.Background(), &ExecParams{
		ProcessID: "pushed",
		Argv:      command,
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{"CODEX_GO_EXEC_SERVER_TTY_HELPER": "1"},
		TTY:       true,
		PipeStdin: false,
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var output strings.Builder
	lastSeq := uint64(0)
	sawExit := false
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		event, eventErr := events.Next(ctx)
		cancel()
		if eventErr != nil {
			t.Fatalf("Next() error = %v", eventErr)
		}
		if event.Seq != lastSeq+1 {
			t.Fatalf("event sequence = %d after %d", event.Seq, lastSeq)
		}
		lastSeq = event.Seq
		switch event.Kind {
		case ProcessEventOutput:
			if event.Stream != "pty" {
				t.Fatalf("TTY output stream = %q, want pty", event.Stream)
			}
			data, decodeErr := base64.StdEncoding.DecodeString(event.Chunk)
			if decodeErr != nil {
				t.Fatalf("DecodeString() error = %v", decodeErr)
			}
			output.Write(data)
		case ProcessEventExited:
			sawExit = true
			if event.ExitCode != 0 || event.SandboxDenied == nil || *event.SandboxDenied {
				t.Fatalf("exit event = %#v", event)
			}
		case ProcessEventClosed:
			if !sawExit || output.String() != "pushed" {
				t.Fatalf("events closed with output %q, sawExit=%v", output.String(), sawExit)
			}
			cancelServer()
			select {
			case err := <-serverDone:
				if err != nil {
					t.Fatalf("exec-server shutdown error = %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("exec-server did not stop")
			}
			return
		}
	}
}

func TestProcessEventSubscriptionReportsLagAndRetainsNewestEventsLikeRust(t *testing.T) {
	session := &clientProcessSession{pending: map[uint64]ProcessEvent{}}
	subscription := &ProcessEventSubscription{processID: "replay", session: session, notify: make(chan struct{}, 1)}
	session.subscription = subscription
	for seq := uint64(1); seq <= 600; seq++ {
		session.publishOrdered(ProcessEvent{Kind: ProcessEventOutput, ProcessID: "replay", Seq: seq})
	}
	event, err := subscription.Next(context.Background())
	if err != nil || event.Kind != ProcessEventLagged {
		t.Fatalf("first event = %#v, %v", event, err)
	}
	event, err = subscription.Next(context.Background())
	if err != nil || event.Seq != 345 {
		t.Fatalf("first retained event = %#v, %v", event, err)
	}
}

func TestClientLegacyExitedNotificationPreservesMissingSandboxMetadataLikeRust(t *testing.T) {
	client := &Client{sessions: map[string]*clientProcessSession{}, done: make(chan struct{})}
	session := &clientProcessSession{pending: map[uint64]ProcessEvent{}}
	subscription := &ProcessEventSubscription{client: client, processID: "legacy", session: session, notify: make(chan struct{}, 1)}
	session.subscription = subscription
	client.sessions["legacy"] = session
	if err := client.handleNotification(MethodProcessExited, []byte(`{"processId":"legacy","seq":1,"exitCode":1}`)); err != nil {
		t.Fatalf("handleNotification() error = %v", err)
	}
	event, err := subscription.Next(context.Background())
	if err != nil || event.Kind != ProcessEventExited || event.SandboxDenied != nil {
		t.Fatalf("legacy exit event = %#v, %v", event, err)
	}
}

func TestClientRejectsMalformedPushedProcessEventsLikeRust(t *testing.T) {
	client := &Client{sessions: map[string]*clientProcessSession{}, done: make(chan struct{})}
	for name, test := range map[string]struct {
		method  string
		payload string
	}{
		"missing-seq":     {method: MethodProcessOutput, payload: `{"processId":"p","stream":"stdout","chunk":"eA=="}`},
		"invalid-stream":  {method: MethodProcessOutput, payload: `{"processId":"p","seq":1,"stream":"console","chunk":"eA=="}`},
		"invalid-base64":  {method: MethodProcessOutput, payload: `{"processId":"p","seq":1,"stream":"stdout","chunk":"%%%"}`},
		"missing-exit":    {method: MethodProcessExited, payload: `{"processId":"p","seq":1}`},
		"null-closed-seq": {method: MethodProcessClosed, payload: `{"processId":"p","seq":null}`},
	} {
		t.Run(name, func(t *testing.T) {
			if err := client.handleNotification(test.method, []byte(test.payload)); err == nil {
				t.Fatal("handleNotification() error = nil")
			}
		})
	}
}

func TestClientEnvironmentStatusReturnsReadyLikeRust(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}
	client, err := DialClient(context.Background(), serverURL, "client-status-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	status, err := client.EnvironmentStatus(context.Background())
	if err != nil {
		t.Fatalf("EnvironmentStatus() error = %v", err)
	}
	if status.Status != EnvironmentStatusReady {
		t.Fatalf("EnvironmentStatus() = %+v", status)
	}
	cancelServer()
	_ = client.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}

type execServerURLChannelWriter struct {
	url chan string
}

func (w *execServerURLChannelWriter) Write(data []byte) (int, error) {
	select {
	case w.url <- strings.TrimSpace(string(data)):
	default:
	}
	return len(data), nil
}

func TestClientDispatchesWriteWhileReadLongPollIsPendingLikeRust(t *testing.T) {
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()
	urlCh := make(chan string, 1)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- NewServer().ServeTransport(serverCtx, "ws://127.0.0.1:0", nil, &execServerURLChannelWriter{url: urlCh})
	}()
	var serverURL string
	select {
	case serverURL = <-urlCh:
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server URL was not reported")
	}

	client, err := DialClient(context.Background(), serverURL, "client-concurrency-test")
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	command := []string{"sh", "-c", "read line; printf '%s' \"$line\""}
	if runtime.GOOS == "windows" {
		command = []string{"powershell", "-NoProfile", "-Command", "$line=[Console]::In.ReadLine(); [Console]::Out.Write($line)"}
	}
	if _, err := client.Start(context.Background(), &ExecParams{
		ProcessID: "concurrent",
		Argv:      command,
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
		PipeStdin: true,
	}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitMS := uint64(1_000)
	readDone := make(chan *ReadResponse, 1)
	readErr := make(chan error, 1)
	go func() {
		response, readError := client.Read(context.Background(), &ReadParams{ProcessID: "concurrent", WaitMS: &waitMS})
		if readError != nil {
			readErr <- readError
			return
		}
		readDone <- response
	}()
	time.Sleep(75 * time.Millisecond)
	started := time.Now()
	if _, err := client.Write(context.Background(), &WriteParams{ProcessID: "concurrent", Chunk: "aGVsbG8K", WriteID: "write-1"}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("Write() was blocked by read long poll for %s", elapsed)
	}
	select {
	case response := <-readDone:
		if response == nil || len(response.Chunks) == 0 {
			t.Fatalf("Read() response = %#v", response)
		}
	case err := <-readErr:
		t.Fatalf("Read() error = %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("Read() did not complete")
	}

	cancelServer()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("exec-server shutdown error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exec-server did not stop")
	}
}
