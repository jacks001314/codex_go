package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/internal/auth"
)

func TestMCPServerInitialize(t *testing.T) {
	var stdout bytes.Buffer
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"test","version":"1"}}}` + "\n"
	if err := Run(context.Background(), []string{"mcp-server"}, strings.NewReader(input), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp-server returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Content-Length:") ||
		!strings.Contains(stdout.String(), `"id":1`) ||
		!strings.Contains(stdout.String(), `"name":"codex-mcp-server"`) ||
		!strings.Contains(stdout.String(), `"listChanged":true`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestMCPServerStrictConfigRejectsUnknownConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("foo = \"bar\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	err := Run(context.Background(), []string{
		"mcp-server",
		"--strict-config",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown configuration field `foo`") {
		t.Fatalf("mcp-server strict config error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(home, "config.toml"), nil, 0o600); err != nil {
		t.Fatalf("clear config returned error: %v", err)
	}
	err = Run(context.Background(), []string{
		"--strict-config",
		"-c", "foo=bar",
		"mcp-server",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown configuration field `foo`") {
		t.Fatalf("mcp-server root strict override error = %v", err)
	}
}

func TestCloudExecAndList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := auth.NewStore(home).Save(auth.FromAccessToken("test-cloud-token")); err != nil {
		t.Fatalf("Save auth error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-cloud-token" {
			t.Fatalf("Authorization header = %q", got)
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/codex/tasks":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode create request error = %v", err)
			}
			newTask, _ := request["new_task"].(map[string]any)
			if newTask["environment_id"] != "env-1" {
				t.Fatalf("create request = %#v", request)
			}
			metadata, _ := request["metadata"].(map[string]any)
			if metadata["best_of_n"] != float64(2) {
				t.Fatalf("metadata = %#v", metadata)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"task":{"id":"task-1"}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/codex/tasks/list":
			if got := r.URL.Query().Get("task_filter"); got != "current" {
				t.Fatalf("task_filter = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{
				"items": [{
					"id": "task-1",
					"title": "Fix tests",
					"status": "ready",
					"updated_at": "2026-06-30T00:00:00Z",
					"environment_id": "env-1",
					"environment_label": "Env One",
					"summary": {"files_changed": 1, "lines_added": 2, "lines_removed": 1}
				}],
				"cursor": "next"
			}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	t.Setenv("CODEX_CLOUD_TASKS_BASE_URL", server.URL)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"cloud", "exec", "--env", "env-1", "--attempts", "2", "fix", "tests"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("cloud exec returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, server.URL+"/codex/tasks/task-1") {
		t.Fatalf("exec stdout = %q", output)
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"cloud", "list", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("cloud list returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"id": "task-1"`) || !strings.Contains(stdout.String(), `"cursor": "next"`) {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"cloud", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("cloud list returned error: %v", err)
	}
	output = stdout.String()
	if !strings.Contains(output, server.URL+"/codex/tasks/task-1") ||
		!strings.Contains(output, "[READY] Fix tests") ||
		!strings.Contains(output, "Env One") ||
		!strings.Contains(output, "+2/-1  -  1 file") ||
		!strings.Contains(output, `codex cloud list --cursor="next"`) {
		t.Fatalf("list stdout = %q", output)
	}
}

func TestCloudDefaultBrowsesTasks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := auth.NewStore(home).Save(auth.FromAccessToken("test-cloud-token")); err != nil {
		t.Fatalf("Save auth error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-cloud-token" {
			t.Fatalf("Authorization header = %q", got)
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/codex/tasks/list" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"items": [{
				"id": "task-2",
				"title": "Add cloud browser",
				"status": "pending",
				"updated_at": "2026-06-30T00:00:00Z",
				"environment_label": "Env Two",
				"summary": {"files_changed": 0, "lines_added": 0, "lines_removed": 0}
			}]
		}`)
	}))
	defer server.Close()
	t.Setenv("CODEX_CLOUD_TASKS_BASE_URL", server.URL)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"cloud"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("cloud returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Codex Cloud tasks") ||
		!strings.Contains(output, server.URL+"/codex/tasks/task-2") ||
		!strings.Contains(output, "[PENDING] Add cloud browser") ||
		!strings.Contains(output, "no diff") {
		t.Fatalf("cloud stdout = %q", output)
	}
}

func TestCloudDiffSelectsAttempt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := auth.NewStore(home).Save(auth.FromAccessToken("test-cloud-token")); err != nil {
		t.Fatalf("Save auth error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-cloud-token" {
			t.Fatalf("Authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/codex/tasks/task-3":
			_, _ = io.WriteString(w, `{
				"task": {"id": "task-3", "title": "Attempted"},
				"current_assistant_turn": {
					"id": "turn-1",
					"attempt_placement": 1,
					"sibling_turn_ids": ["turn-2"],
					"output_items": [{"type":"output_diff","diff":"diff --git a/one b/one"}]
				}
			}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/codex/tasks/task-3/turns/turn-1/sibling_turns":
			_, _ = io.WriteString(w, `{
				"sibling_turns": [{
					"id": "turn-2",
					"attempt_placement": 2,
					"created_at": "2026-06-30T00:00:01Z",
					"turn_status": "completed",
					"output_items": [{"type":"output_diff","diff":"diff --git a/two b/two"}]
				}]
			}`)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	t.Setenv("CODEX_CLOUD_TASKS_BASE_URL", server.URL)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"cloud", "diff", "--attempt", "2", "task-3"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("cloud diff returned error: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "diff --git a/two b/two" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestResponsesAPIProxyWritesServerInfo(t *testing.T) {
	exitCodes := stubResponsesAPIProxyExit(t)
	dir := t.TempDir()
	serverInfo := filepath.Join(dir, "nested", "server.json")
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), []string{
			"responses-api-proxy",
			"--port", "0",
			"--server-info", serverInfo,
			"--http-shutdown",
			"--upstream-url", "http://127.0.0.1/responses",
		}, strings.NewReader("sk_proxy_test\n"), &stdout, &stderr)
	}()
	port := waitForResponsesAPIProxyInfo(t, serverInfo)
	resp, err := http.Get("http://127.0.0.1:" + port + "/shutdown")
	if err != nil {
		t.Fatalf("GET shutdown error = %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("ReadAll shutdown response error = %v", err)
	}
	if string(body) != "" {
		t.Fatalf("shutdown body = %q", string(body))
	}
	select {
	case code := <-exitCodes:
		if code != 0 {
			t.Fatalf("exit code = %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not call process exit hook")
	}
	if err := <-done; err != nil {
		t.Fatalf("responses-api-proxy returned error: %v", err)
	}
	if !strings.Contains(stderr.String(), "responses-api-proxy listening") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(serverInfo)
	if err != nil {
		t.Fatalf("ReadFile server info error = %v", err)
	}
	var info struct {
		Port int `json:"port"`
		PID  int `json:"pid"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("Unmarshal server info error = %v", err)
	}
	if got := strings.Count(string(data), "\n"); got != 1 || !strings.HasSuffix(string(data), "\n") || info.Port == 0 || info.PID == 0 {
		t.Fatalf("server info = %q parsed=%#v", string(data), info)
	}
}

func TestResponsesAPIProxyRejectsInvalidUpstreamURLLikeRust(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{name: "relative", url: "api.openai.test/v1/responses", want: "parsing --upstream-url: relative URL without a base"},
		{name: "missing host", url: "file:///tmp/responses", want: "upstream URL must include a host"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Run(context.Background(), []string{
				"responses-api-proxy",
				"--upstream-url", tc.url,
			}, strings.NewReader("sk_proxy_test\n"), &bytes.Buffer{}, &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestResponsesAPIProxyForwardsResponsesRequest(t *testing.T) {
	stubResponsesAPIProxyExit(t)
	upstreamHost := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("upstream path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk_proxy_test" {
			t.Fatalf("upstream Authorization = %q", got)
		}
		if got := r.Host; got != upstreamHost {
			t.Fatalf("upstream Host = %q", got)
		}
		w.Header().Set("X-Upstream", "ok")
		w.Header().Set("Content-Length", "15")
		_, _ = io.WriteString(w, `{"id":"resp-1"}`)
	}))
	defer upstream.Close()
	upstreamHost = strings.TrimPrefix(upstream.URL, "http://")

	dir := t.TempDir()
	serverInfo := filepath.Join(dir, "server.json")
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), []string{
			"responses-api-proxy",
			"--port", "0",
			"--server-info", serverInfo,
			"--http-shutdown",
			"--upstream-url", upstream.URL + "/responses",
		}, strings.NewReader("sk_proxy_test\n"), &stdout, &stderr)
	}()
	port := waitForResponsesAPIProxyInfo(t, serverInfo)
	resp, err := http.Post("http://127.0.0.1:"+port+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-test"}`))
	if err != nil {
		t.Fatalf("POST responses error = %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("ReadAll response error = %v", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"resp-1"`) || resp.Header.Get("X-Upstream") != "ok" {
		t.Fatalf("proxy response status=%d header=%q body=%q", resp.StatusCode, resp.Header.Get("X-Upstream"), string(body))
	}
	if got := resp.Header.Get("Content-Length"); got != "15" {
		t.Fatalf("proxy Content-Length = %q", got)
	}
	shutdown, err := http.Get("http://127.0.0.1:" + port + "/shutdown")
	if err != nil {
		t.Fatalf("GET shutdown error = %v", err)
	}
	_ = shutdown.Body.Close()
	if err := <-done; err != nil {
		t.Fatalf("responses-api-proxy returned error: %v", err)
	}
}

func TestResponsesAPIProxyStreamsResponsesRequest(t *testing.T) {
	stubResponsesAPIProxyExit(t)
	firstChunkWritten := make(chan struct{})
	finish := make(chan struct{})
	var finishOnce sync.Once
	releaseUpstream := func() { finishOnce.Do(func() { close(finish) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		close(firstChunkWritten)
		<-finish
		_, _ = io.WriteString(w, "data: two\n\n")
	}))
	defer upstream.Close()
	defer releaseUpstream()

	dir := t.TempDir()
	serverInfo := filepath.Join(dir, "server.json")
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), []string{
			"responses-api-proxy",
			"--port", "0",
			"--server-info", serverInfo,
			"--http-shutdown",
			"--upstream-url", upstream.URL + "/responses",
		}, strings.NewReader("sk_proxy_test\n"), &stdout, &stderr)
	}()
	port := waitForResponsesAPIProxyInfo(t, serverInfo)
	resp, err := http.Post("http://127.0.0.1:"+port+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-test"}`))
	if err != nil {
		t.Fatalf("POST responses error = %v", err)
	}
	defer resp.Body.Close()
	select {
	case <-firstChunkWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not write first chunk")
	}
	buffer := make([]byte, len("data: one\n\n"))
	if _, err := io.ReadFull(resp.Body, buffer); err != nil {
		t.Fatalf("ReadFull first chunk error = %v", err)
	}
	if string(buffer) != "data: one\n\n" {
		t.Fatalf("first chunk = %q", string(buffer))
	}
	releaseUpstream()
	shutdown, err := http.Get("http://127.0.0.1:" + port + "/shutdown")
	if err != nil {
		t.Fatalf("GET shutdown error = %v", err)
	}
	_ = shutdown.Body.Close()
	if err := <-done; err != nil {
		t.Fatalf("responses-api-proxy returned error: %v", err)
	}
}

func TestResponsesAPIProxyDumpsPartialResponseAfterClientDisconnect(t *testing.T) {
	stubResponsesAPIProxyExit(t)
	firstChunkWritten := make(chan struct{})
	finish := make(chan struct{})
	var finishOnce sync.Once
	releaseUpstream := func() { finishOnce.Do(func() { close(finish) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		close(firstChunkWritten)
		<-finish
		_, _ = io.WriteString(w, "data: two\n\n")
	}))
	defer upstream.Close()
	defer releaseUpstream()

	dir := t.TempDir()
	dumpDir := filepath.Join(dir, "dumps")
	serverInfo := filepath.Join(dir, "server.json")
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), []string{
			"responses-api-proxy",
			"--port", "0",
			"--server-info", serverInfo,
			"--http-shutdown",
			"--upstream-url", upstream.URL + "/responses",
			"--dump-dir", dumpDir,
		}, strings.NewReader("sk_proxy_test\n"), &stdout, &stderr)
	}()
	port := waitForResponsesAPIProxyInfo(t, serverInfo)
	resp, err := http.Post("http://127.0.0.1:"+port+"/v1/responses", "application/json", strings.NewReader(`{"model":"gpt-test"}`))
	if err != nil {
		t.Fatalf("POST responses error = %v", err)
	}
	select {
	case <-firstChunkWritten:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not write first chunk")
	}
	buffer := make([]byte, len("data: one\n\n"))
	if _, err := io.ReadFull(resp.Body, buffer); err != nil {
		t.Fatalf("ReadFull first chunk error = %v", err)
	}
	_ = resp.Body.Close()
	releaseUpstream()

	responseDump := waitForDumpFileWithSuffix(t, dumpDir, "-response.json")
	data, err := os.ReadFile(responseDump)
	if err != nil {
		t.Fatalf("ReadFile response dump error = %v", err)
	}
	if !strings.Contains(string(data), "data: one") {
		t.Fatalf("response dump = %s", data)
	}

	shutdown, err := http.Get("http://127.0.0.1:" + port + "/shutdown")
	if err != nil {
		t.Fatalf("GET shutdown error = %v", err)
	}
	_ = shutdown.Body.Close()
	if err := <-done; err != nil {
		t.Fatalf("responses-api-proxy returned error: %v; stderr=%q", err, stderr.String())
	}
}

func TestResponsesAPIProxyHeaderCopyMatchesRust(t *testing.T) {
	forwardSrc := http.Header{}
	forwardSrc.Set("Authorization", "Bearer client")
	forwardSrc.Set("Host", "client.example")
	forwardSrc.Set("Connection", "keep-alive")
	forwardSrc.Set("Te", "trailers")
	forwardSrc.Set("X-Custom", "ok")
	forwardDst := http.Header{}
	copyForwardHeaders(forwardDst, forwardSrc)
	if forwardDst.Get("Authorization") != "" || forwardDst.Get("Host") != "" {
		t.Fatalf("forward headers leaked auth/host: %#v", forwardDst)
	}
	if forwardDst.Get("Connection") != "keep-alive" || forwardDst.Get("Te") != "trailers" || forwardDst.Get("X-Custom") != "ok" {
		t.Fatalf("forward headers = %#v", forwardDst)
	}

	responseSrc := http.Header{}
	responseSrc.Set("Content-Length", "123")
	responseSrc.Set("Transfer-Encoding", "chunked")
	responseSrc.Set("Connection", "close")
	responseSrc.Set("Trailer", "x-later")
	responseSrc.Set("Upgrade", "websocket")
	responseSrc.Set("Keep-Alive", "timeout=5")
	responseSrc.Set("Proxy-Authenticate", "Basic realm=test")
	responseSrc.Set("X-Upstream", "ok")
	responseDst := http.Header{}
	copyResponseHeaders(responseDst, responseSrc)
	for _, name := range []string{"Content-Length", "Transfer-Encoding", "Connection", "Trailer", "Upgrade"} {
		if got := responseDst.Get(name); got != "" {
			t.Fatalf("response header %s = %q", name, got)
		}
	}
	if responseDst.Get("Keep-Alive") != "timeout=5" ||
		responseDst.Get("Proxy-Authenticate") != "Basic realm=test" ||
		responseDst.Get("X-Upstream") != "ok" {
		t.Fatalf("response headers = %#v", responseDst)
	}
}

func TestResponsesAPIProxyAuthHeaderReadsLikeRust(t *testing.T) {
	header, err := readResponsesAPIProxyAuthHeader(strings.NewReader("sk_proxy_test\r\nignored"))
	if err != nil {
		t.Fatalf("readResponsesAPIProxyAuthHeader returned error: %v", err)
	}
	if header != "Bearer sk_proxy_test" {
		t.Fatalf("header = %q", header)
	}

	_, err = readResponsesAPIProxyAuthHeader(strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "API key must be provided via stdin") {
		t.Fatalf("empty key error = %v", err)
	}

	_, err = readResponsesAPIProxyAuthHeader(strings.NewReader(" sk_proxy_test\n"))
	if err == nil || !strings.Contains(err.Error(), "API key may only contain ASCII letters") {
		t.Fatalf("space key error = %v", err)
	}

	oversized := strings.Repeat("a", responsesAPIProxyAuthHeaderBufferSize-len("Bearer "))
	_, err = readResponsesAPIProxyAuthHeader(strings.NewReader(oversized))
	if err == nil || !strings.Contains(err.Error(), "API key is too large to fit in the 1024-byte buffer") {
		t.Fatalf("oversized key error = %v", err)
	}
}

func waitForResponsesAPIProxyInfo(t *testing.T, path string) string {
	t.Helper()
	var data []byte
	var err error
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		data, err = os.ReadFile(path)
		if err == nil {
			var info struct {
				Port int `json:"port"`
			}
			if err := json.Unmarshal(data, &info); err != nil {
				t.Fatalf("Unmarshal server info error = %v", err)
			}
			if info.Port > 0 {
				return fmt.Sprintf("%d", info.Port)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server info not written: %v; data=%q", err, string(data))
	return ""
}

func waitForDumpFileWithSuffix(t *testing.T, dir, suffix string) string {
	t.Helper()
	var lastErr error
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		entries, err := os.ReadDir(dir)
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), suffix) {
				return filepath.Join(dir, entry.Name())
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("dump file %s not written in %s: %v", suffix, dir, lastErr)
	return ""
}

func stubResponsesAPIProxyExit(t *testing.T) <-chan int {
	t.Helper()
	exitCodes := make(chan int, 1)
	previous := responsesAPIProxyExit
	responsesAPIProxyExit = func(code int) {
		exitCodes <- code
	}
	t.Cleanup(func() {
		responsesAPIProxyExit = previous
	})
	return exitCodes
}

func TestExecServerConfigOutput(t *testing.T) {
	var stdout bytes.Buffer
	input := `{"id":1,"method":"initialize","params":{"clientName":"app-test"}}` + "\n"
	if err := Run(context.Background(), []string{
		"exec-server",
		"--listen", "stdio",
	}, strings.NewReader(input), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("exec-server returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"id":1`) || !strings.Contains(output, `"sessionId"`) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestExecServerWebSocketListenStartsServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, []string{
			"exec-server",
			"--listen", "ws://127.0.0.1:0",
		}, strings.NewReader(""), &stdout, &stderr)
	}()
	listenURL := waitForAppExecServerListenURL(t, &stdout, errCh)
	if strings.Contains(stdout.String(), `"listen"`) {
		t.Fatalf("exec-server wrote old placeholder JSON: %q", stdout.String())
	}

	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + strings.TrimPrefix(listenURL, "ws://") + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz error = %v; stderr=%q", err, stderr.String())
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("/readyz status = %d; stderr=%q", response.StatusCode, stderr.String())
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("exec-server returned error after cancel: %v; stderr=%q", err, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatalf("exec-server did not stop after cancel; stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestExecServerExplicitEmptyListenIsRejectedLikeRust(t *testing.T) {
	err := Run(context.Background(), []string{
		"exec-server",
		"--listen=",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "unsupported --listen URL ``; expected `ws://IP:PORT` or `stdio`" {
		t.Fatalf("exec-server empty listen error = %v", err)
	}
}

func TestExecServerStrictConfigRejectsUnknownConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("foo = \"bar\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	err := Run(context.Background(), []string{
		"exec-server",
		"--strict-config",
		"--listen", "ws://127.0.0.1:0",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown configuration field `foo`") {
		t.Fatalf("exec-server strict config error = %v", err)
	}

	err = Run(context.Background(), []string{
		"--strict-config",
		"-c", "foo=bar",
		"exec-server",
		"--listen", "ws://127.0.0.1:0",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown configuration field `foo`") {
		t.Fatalf("exec-server root strict override error = %v", err)
	}
}

func TestExecServerRemoteValidationLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")

	err := Run(context.Background(), []string{
		"exec-server",
		"--remote", "https://api.openai.com",
		"--environment-id", "env-1",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "remote exec-server registration requires ChatGPT authentication or API key authentication") {
		t.Fatalf("exec-server remote no-auth error = %v", err)
	}

	t.Setenv(auth.CodexAPIKeyEnv, "sk-test")
	err = Run(context.Background(), []string{
		"exec-server",
		"--remote", "http://api.openai.com",
		"--environment-id", "env-1",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "remote exec-server API-key authentication is restricted") {
		t.Fatalf("exec-server remote api-key host error = %v", err)
	}

	err = Run(context.Background(), []string{
		"exec-server",
		"--remote", "https://service.openai.org.evil.example/api",
		"--environment-id", "env-1",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "remote exec-server API-key authentication is restricted") {
		t.Fatalf("exec-server remote suffix-spoof error = %v", err)
	}

	var stdout bytes.Buffer
	err = Run(context.Background(), []string{
		"exec-server",
		"--remote", "https://api.openai.com/",
		"--environment-id", "env-1",
		"--name", "worker-a",
	}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "remote exec-server registration is not implemented in codex_go") {
		t.Fatalf("exec-server remote unsupported error = %v", err)
	}
	if stdout.String() != "" {
		t.Fatalf("stdout = %q", stdout.String())
	}

	t.Setenv(auth.CodexAPIKeyEnv, "")
	err = Run(context.Background(), []string{
		"exec-server",
		"--remote", "https://api.openai.com",
		"--environment-id", "env-1",
		"--use-agent-identity-auth",
	}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "CODEX_ACCESS_TOKEN is required when --use-agent-identity-auth is set") {
		t.Fatalf("exec-server remote agent identity env error = %v", err)
	}
}

func waitForAppExecServerListenURL(t *testing.T, stdout *bytes.Buffer, errCh <-chan error) string {
	t.Helper()
	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-errCh:
			t.Fatalf("exec-server exited before writing listen URL: %v; stdout=%q", err, stdout.String())
		case <-deadline:
			t.Fatalf("exec-server listen URL not written: stdout=%q", stdout.String())
		case <-ticker.C:
			line := strings.TrimSpace(stdout.String())
			if strings.HasPrefix(line, "ws://") {
				return line
			}
		}
	}
}

func TestDesktopAppCommandOutput(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"app", "--download-url", "https://example.test/codex", "."}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("app returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"status": "ready"`) || !strings.Contains(output, `"downloadUrl": "https://example.test/codex"`) {
		t.Fatalf("stdout = %q", output)
	}
}

func TestUpdateCommandRejectsJSONFlag(t *testing.T) {
	err := Run(context.Background(), []string{"update", "--json"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown update option --json") {
		t.Fatalf("update --json error = %v", err)
	}
}
