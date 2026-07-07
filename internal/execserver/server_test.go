package execserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestStdioInitializeAndEnvironmentInfo(t *testing.T) {
	input := `{"id":1,"method":"initialize","params":{"clientName":"test"}}` + "\n" +
		`{"id":2,"method":"environment/info","params":{}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"sessionId"`) || !strings.Contains(output, `"shell"`) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestStdioProcessStartReadAndWriteClosed(t *testing.T) {
	input := `{"id":1,"method":"initialize","params":{"clientName":"test"}}` + "\n" +
		`{"id":2,"method":"process/start","params":{"processId":"proc-1","argv":["go","version"],"cwd":"","env":{},"tty":false}}` + "\n" +
		`{"id":3,"method":"process/write","params":{"processId":"proc-1","chunk":"` + base64.StdEncoding.EncodeToString([]byte("ignored\n")) + `","writeId":"w1"}}` + "\n" +
		`{"id":4,"method":"process/read","params":{"processId":"proc-1","afterSeq":0}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"processId":"proc-1"`) || !strings.Contains(output, `"status":"stdinClosed"`) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestStdioJSONRPCProtocolErrors(t *testing.T) {
	input := `{"id":1,"method":"unknown/method","params":{}}` + "\n" +
		`{"id":2,"method":"process/read","params":{"processId":123}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"code":-32601`) || !strings.Contains(output, `"unknown exec-server method unknown/method"`) {
		t.Fatalf("missing unknown method error: %q", output)
	}
	if !strings.Contains(output, `"code":-32602`) || !strings.Contains(output, `"invalid params:`) {
		t.Fatalf("missing invalid params error: %q", output)
	}
}

func TestStdioNotificationWithNestedIDDoesNotRequireRequestID(t *testing.T) {
	input := `{"method":"initialized","params":{"id":"nested-only"}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no response for notification", stdout.String())
	}
}

func TestStdioProcessStartRejectsUnsupportedSandboxAndManagedNetwork(t *testing.T) {
	input := `{"id":1,"method":"process/start","params":{"processId":"sandboxed","argv":["go","version"],"cwd":"","env":{},"sandbox":{"mode":"readOnly"}}}` + "\n" +
		`{"id":2,"method":"process/start","params":{"processId":"networked","argv":["go","version"],"cwd":"","env":{},"enforceManagedNetwork":true}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"code":-32600`) || !strings.Contains(output, "sandbox is not supported") || !strings.Contains(output, "managed network is not supported") {
		t.Fatalf("stdout = %q", output)
	}
}

func TestStdioFileSystemMethods(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "hello.txt")
	encoded := base64.StdEncoding.EncodeToString([]byte("hello"))
	input := `{"id":1,"method":"fs/createDirectory","params":{"path":` + quote(filepath.Dir(path)) + `,"recursive":true}}` + "\n" +
		`{"id":2,"method":"fs/writeFile","params":{"path":` + quote(path) + `,"dataBase64":` + quote(encoded) + `}}` + "\n" +
		`{"id":3,"method":"fs/readFile","params":{"path":` + quote(path) + `}}` + "\n" +
		`{"id":4,"method":"fs/getMetadata","params":{"path":` + quote(path) + `}}` + "\n" +
		`{"id":5,"method":"fs/readDirectory","params":{"path":` + quote(filepath.Dir(path)) + `}}` + "\n" +
		`{"id":6,"method":"fs/canonicalize","params":{"path":` + quote(path) + `}}` + "\n" +
		`{"id":7,"method":"fs/open","params":{"handleId":"h1","path":` + quote(path) + `}}` + "\n" +
		`{"id":8,"method":"fs/readBlock","params":{"handleId":"h1","offset":1,"len":3}}` + "\n" +
		`{"id":9,"method":"fs/close","params":{"handleId":"h1"}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, encoded) || !strings.Contains(output, `"isFile":true`) || !strings.Contains(output, `"fileName":"hello.txt"`) || !strings.Contains(output, base64.StdEncoding.EncodeToString([]byte("ell"))) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestStdioFileSystemWalk(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("root"), 0o600); err != nil {
		t.Fatalf("WriteFile(root) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "note.txt"), []byte("nested"), 0o600); err != nil {
		t.Fatalf("WriteFile(nested) error = %v", err)
	}

	input := `{"id":1,"method":"fs/walk","params":{"path":` + quote(dir) + `,"options":{"maxDepth":4,"maxDirectories":10,"maxEntries":10,"followDirectorySymlinks":false}}}` + "\n" +
		`{"id":2,"method":"fs/walk","params":{"path":` + quote(dir) + `,"options":{"maxDepth":4,"maxDirectories":1,"maxEntries":10,"followDirectorySymlinks":false}}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"kind":"directory"`) || !strings.Contains(output, `"kind":"file"`) || !strings.Contains(output, `"truncated":true`) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestWalkPathEmptyDirectoryUsesEmptyArrays(t *testing.T) {
	response, err := walkPath(&FSWalkParams{Path: t.TempDir()})
	if err != nil {
		t.Fatalf("walkPath() error = %v", err)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"entries":[]`) || !strings.Contains(string(data), `"errors":[]`) {
		t.Fatalf("walk response = %s", data)
	}
}

func TestStdioProcessReadWaitsForOutput(t *testing.T) {
	shell, args := waitOutputCommand()
	input := `{"id":1,"method":"process/start","params":{"processId":"proc-wait","argv":[` + quote(shell)
	for _, arg := range args {
		input += `,` + quote(arg)
	}
	input += `],"cwd":"","env":{},"tty":false,"pipeStdin":false}}` + "\n" +
		`{"id":2,"method":"process/read","params":{"processId":"proc-wait","afterSeq":0,"waitMs":1500}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	var read *ReadResponse
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		var envelope struct {
			ID     int           `json:"id"`
			Result *ReadResponse `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &envelope); err != nil {
			continue
		}
		if envelope.ID == 2 {
			read = envelope.Result
			break
		}
	}
	if read == nil {
		t.Fatalf("read response missing in %q", stdout.String())
	}
	if output := decodedChunks(read.Chunks, "stdout"); !strings.Contains(output, "delayed") {
		t.Fatalf("stdout chunks = %q response = %+v raw = %q", output, read, stdout.String())
	}
}

func TestStartProcessHonorsArg0(t *testing.T) {
	server := NewServer()
	arg0 := "codex-custom-arg0"
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: "proc-arg0",
		Argv:      []string{os.Args[0], "-test.run=TestExecServerArg0Helper"},
		CWD:       "",
		EnvPolicy: &ExecEnvPolicy{
			Inherit:               "core",
			IgnoreDefaultExcludes: true,
		},
		Env:  map[string]string{"CODEX_EXEC_SERVER_ARG0_HELPER": "1"},
		Arg0: &arg0,
	}); err != nil {
		t.Fatalf("startProcess() error = %v", err)
	}
	after := uint64(0)
	wait := uint64(5000)
	read, err := server.readProcess(&ReadParams{ProcessID: "proc-arg0", AfterSeq: &after, WaitMS: &wait})
	if err != nil {
		t.Fatalf("readProcess() error = %v", err)
	}
	output := decodedChunks(read.Chunks, "stdout")
	if output != arg0 {
		t.Fatalf("stdout = %q, want arg0 %q", output, arg0)
	}
}

func TestExecServerArg0Helper(t *testing.T) {
	if os.Getenv("CODEX_EXEC_SERVER_ARG0_HELPER") != "1" {
		return
	}
	_, _ = os.Stdout.WriteString(os.Args[0])
	os.Exit(0)
}

func TestChildEnvPolicy(t *testing.T) {
	params := &ExecParams{
		EnvPolicy: &ExecEnvPolicy{
			Inherit:               "none",
			IgnoreDefaultExcludes: true,
			Set:                   map[string]string{"FROM_POLICY": "yes"},
		},
		Env: map[string]string{"FROM_ENV": "yes"},
	}
	env := childEnv(params)
	wantLen := 2
	if runtime.GOOS == "windows" {
		wantLen = 3
		if env["PATHEXT"] != ".COM;.EXE;.BAT;.CMD" {
			t.Fatalf("childEnv() = %#v", env)
		}
	}
	if env["FROM_POLICY"] != "yes" || env["FROM_ENV"] != "yes" || len(env) != wantLen {
		t.Fatalf("childEnv() = %#v", env)
	}
}

func TestCopyPathPreservesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on Windows")
	}
	root := t.TempDir()
	source := filepath.Join(root, "src")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "target.txt"), []byte("target"), 0o600); err != nil {
		t.Fatalf("WriteFile(target) error = %v", err)
	}
	if err := os.Symlink("target.txt", filepath.Join(source, "target-link")); err != nil {
		t.Fatalf("Symlink(target-link) error = %v", err)
	}

	destination := filepath.Join(root, "dst")
	if _, err := copyPath(&FSCopyParams{SourcePath: source, DestinationPath: destination, Recursive: true}); err != nil {
		t.Fatalf("copyPath(directory) error = %v", err)
	}
	copiedLink := filepath.Join(destination, "target-link")
	info, err := os.Lstat(copiedLink)
	if err != nil {
		t.Fatalf("Lstat(copiedLink) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("copied link mode = %v, want symlink", info.Mode())
	}
	target, err := os.Readlink(copiedLink)
	if err != nil {
		t.Fatalf("Readlink(copiedLink) error = %v", err)
	}
	if target != "target.txt" {
		t.Fatalf("copied link target = %q, want target.txt", target)
	}

	singleLink := filepath.Join(root, "single-link")
	if _, err := copyPath(&FSCopyParams{SourcePath: filepath.Join(source, "target-link"), DestinationPath: singleLink}); err != nil {
		t.Fatalf("copyPath(symlink) error = %v", err)
	}
	info, err = os.Lstat(singleLink)
	if err != nil {
		t.Fatalf("Lstat(singleLink) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("single copied link mode = %v, want symlink", info.Mode())
	}
}

func TestGetMetadataIncludesCreatedAtOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("createdAtMs is only available through this backend on Windows")
	}
	path := filepath.Join(t.TempDir(), "created.txt")
	if err := os.WriteFile(path, []byte("created"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	metadata, err := getMetadata(&FSGetMetadataParams{Path: path})
	if err != nil {
		t.Fatalf("getMetadata() error = %v", err)
	}
	if metadata.CreatedAtMS == 0 {
		t.Fatalf("createdAtMs = 0, want Windows creation timestamp")
	}
}

func TestRemovePathDefaultsRecursiveAndForce(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "dir")
	if err := os.MkdirAll(filepath.Join(directory, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "nested", "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := removePath(&FSRemoveParams{Path: directory}); err != nil {
		t.Fatalf("removePath(default recursive) error = %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("Stat(directory) error = %v, want not exist", err)
	}
	if _, err := removePath(&FSRemoveParams{Path: filepath.Join(root, "missing")}); err != nil {
		t.Fatalf("removePath(default force missing) error = %v", err)
	}
}

func TestWriteFileInvalidBase64Message(t *testing.T) {
	_, err := writeFile(&FSWriteFileParams{Path: filepath.Join(t.TempDir(), "bad.txt"), DataBase64: "not base64"})
	if err == nil || !strings.Contains(err.Error(), "fs/writeFile requires valid base64 dataBase64") {
		t.Fatalf("writeFile() error = %v", err)
	}
}

func quote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func decodedChunks(chunks []outputChunk, stream string) string {
	var out strings.Builder
	for _, chunk := range chunks {
		if chunk.Stream != stream {
			continue
		}
		data, _ := base64.StdEncoding.DecodeString(chunk.Chunk)
		out.Write(data)
	}
	return out.String()
}

func waitOutputCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "powershell", []string{"-NoProfile", "-Command", "Start-Sleep -Milliseconds 100; Write-Output delayed"}
	}
	return "sh", []string{"-c", "sleep 0.1; printf 'delayed\n'"}
}

func TestStdioHTTPRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("x-codex-test") != "yes" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.Header.Get("x-codex-test"))
		}
		w.Header().Set("x-codex-response", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("response-body"))
	}))
	defer server.Close()

	body := base64.StdEncoding.EncodeToString([]byte("request-body"))
	input := `{"id":1,"method":"http/request","params":{"method":"POST","url":` + quote(server.URL) + `,"headers":[{"name":"x-codex-test","value":"yes"}],"bodyBase64":` + quote(body) + `,"requestId":"http-1"}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	var got struct {
		ID     int                 `json:"id"`
		Result HTTPRequestResponse `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("stdout = %q, unmarshal error = %v", output, err)
	}
	hasHeader := false
	for _, header := range got.Result.Headers {
		if strings.EqualFold(header.Name, "x-codex-response") && header.Value == "ok" {
			hasHeader = true
			break
		}
	}
	if got.Result.Status != http.StatusCreated || got.Result.BodyBase64 != base64.StdEncoding.EncodeToString([]byte("response-body")) || !hasHeader {
		t.Fatalf("stdout = %q", output)
	}
}

func TestHTTPRequestSkipsInvalidHeadersAndSortsResponseHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Good") != "ok" || r.Header.Get("X-Tab") != "ok\tvalue" {
			t.Fatalf("valid headers missing: %#v", r.Header)
		}
		if r.Header.Get("Bad Header") != "" || r.Header.Get("X-Bad-Value") != "" {
			t.Fatalf("invalid headers were sent: %#v", r.Header)
		}
		w.Header().Add("X-Zeta", "2")
		w.Header().Add("X-Alpha", "1")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	response, err := doHTTPRequest(context.Background(), &HTTPRequestParams{
		Method: "GET",
		URL:    server.URL,
		Headers: []HTTPHeader{
			{Name: "X-Good", Value: "ok"},
			{Name: "Bad Header", Value: "bad-name"},
			{Name: "X-Bad-Value", Value: "bad\nvalue"},
			{Name: "X-Tab", Value: "ok\tvalue"},
		},
	})
	if err != nil {
		t.Fatalf("doHTTPRequest() error = %v", err)
	}
	if response.Status != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.Status, http.StatusAccepted)
	}
	previous := ""
	for _, header := range response.Headers {
		current := strings.ToLower(header.Name) + "\x00" + header.Name + "\x00" + header.Value
		if previous != "" && current < previous {
			t.Fatalf("headers not sorted: %#v", response.Headers)
		}
		previous = current
	}
}

func TestStdioHTTPRequestRejectsUnsupportedStreamResponse(t *testing.T) {
	input := `{"id":1,"method":"http/request","params":{"method":"GET","url":"http://127.0.0.1/","streamResponse":true,"requestId":"http-stream"}}` + "\n"
	var stdout bytes.Buffer
	if err := NewServer().Serve(context.Background(), strings.NewReader(input), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"code":-32600`) || !strings.Contains(output, "streamResponse is not supported") {
		t.Fatalf("stdout = %q", output)
	}
}

func TestParseListenURLLikeRust(t *testing.T) {
	tests := []struct {
		name      string
		listenURL string
		want      ListenTransport
		wantErr   string
	}{
		{
			name:      "default",
			listenURL: DefaultListenURL,
			want:      ListenTransport{Kind: ListenKindWebSocket, Address: "127.0.0.1:0"},
		},
		{
			name:      "stdio",
			listenURL: "stdio",
			want:      ListenTransport{Kind: ListenKindStdio},
		},
		{
			name:      "stdio url",
			listenURL: "stdio://",
			want:      ListenTransport{Kind: ListenKindStdio},
		},
		{
			name:      "websocket",
			listenURL: "ws://127.0.0.1:1234",
			want:      ListenTransport{Kind: ListenKindWebSocket, Address: "127.0.0.1:1234"},
		},
		{
			name:      "hostname rejected",
			listenURL: "ws://localhost:1234",
			wantErr:   "invalid websocket --listen URL `ws://localhost:1234`; expected `ws://IP:PORT`",
		},
		{
			name:      "unsupported scheme",
			listenURL: "http://127.0.0.1:1234",
			wantErr:   "unsupported --listen URL `http://127.0.0.1:1234`; expected `ws://IP:PORT` or `stdio`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseListenURL(tt.listenURL)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseListenURL error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseListenURL returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParseListenURL = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestWebSocketTransportServesInitializeAndRejectsOrigin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewServer().ServeTransport(ctx, "ws://127.0.0.1:0", nil, &stdout)
	}()
	listenURL := waitForExecServerListenURL(t, &stdout)

	client := &http.Client{Timeout: time.Second}
	readyURL := "http://" + strings.TrimPrefix(listenURL, "ws://") + "/readyz"
	response, err := client.Get(readyURL)
	if err != nil {
		t.Fatalf("GET /readyz error = %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/readyz status = %d", response.StatusCode)
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Second)
	_, originResponse, err := websocket.Dial(dialCtx, listenURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	dialCancel()
	if err == nil {
		t.Fatal("websocket dial with Origin should fail")
	}
	if originResponse == nil || originResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("origin response = %#v, err = %v", originResponse, err)
	}

	dialCtx, dialCancel = context.WithTimeout(context.Background(), time.Second)
	conn, _, err := websocket.Dial(dialCtx, listenURL, nil)
	dialCancel()
	if err != nil {
		t.Fatalf("websocket dial error = %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), time.Second)
	defer rpcCancel()
	if err := conn.Write(rpcCtx, websocket.MessageText, []byte(`{"id":1,"method":"initialize","params":{"clientName":"ws-test"}}`)); err != nil {
		t.Fatalf("write initialize error = %v", err)
	}
	messageType, data, err := conn.Read(rpcCtx)
	if err != nil {
		t.Fatalf("read initialize response error = %v", err)
	}
	if messageType != websocket.MessageText {
		t.Fatalf("message type = %v", messageType)
	}
	if !bytes.Contains(data, []byte(`"id":1`)) || !bytes.Contains(data, []byte(`"sessionId"`)) {
		t.Fatalf("initialize response = %s", data)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("ServeTransport returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ServeTransport did not stop after context cancellation")
	}
}

func waitForExecServerListenURL(t *testing.T, stdout *bytes.Buffer) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		line := strings.TrimSpace(stdout.String())
		if strings.HasPrefix(line, "ws://") {
			return line
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("exec-server listen URL not written: %q", stdout.String())
	return ""
}
