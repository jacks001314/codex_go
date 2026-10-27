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
	"sync/atomic"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
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

// Rust #55987 covers the exec-server registry auth provider directly: stored
// rotation reuse, forced refresh, refresh failures, and concurrent callers
// sharing one completed refresh (codex-rs/model-provider/src/auth_refresh_tests.rs
// and codex-rs/cli/tests/exec_server/auth_refresh_tests.rs).
func execServerAuthForTest(t *testing.T, home string, client *http.Client) *execServerRegistryAuth {
	t.Helper()
	return newExecServerRegistryAuth(home, auth.StoreOptionsFromConfig("file", false), client)
}

func saveExecServerChatGPTAuthForTest(t *testing.T, home string, tokens map[string]any) {
	t.Helper()
	if err := auth.NewStoreWithOptions(home, auth.StoreOptionsFromConfig("file", false)).Save(auth.AuthDotJSON{
		AuthMode: "chatgpt",
		Tokens:   tokens,
	}); err != nil {
		t.Fatalf("Save(auth) error = %v", err)
	}
}

// TestExecServerRegistryAuthReusesStoredRotationLikeRust checks that ordinary
// requests keep their resolved credential but a rejected one is replaced when
// the store already holds a different credential, without spending the refresh
// token again.
func TestExecServerRegistryAuthReusesStoredRotationLikeRust(t *testing.T) {
	home := t.TempDir()
	saveExecServerChatGPTAuthForTest(t, home, map[string]any{
		"access_token": "old-token", "account_id": "workspace-123", "refresh_token": "refresh-token",
	})
	var refreshCalls atomic.Int32
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		http.Error(w, "refresh must not run", http.StatusInternalServerError)
	}))
	defer refreshServer.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, refreshServer.URL)

	provider := execServerAuthForTest(t, home, refreshServer.Client())
	rejected, err := provider.ResolveAuthHeaders(context.Background())
	if err != nil {
		t.Fatalf("ResolveAuthHeaders() error = %v", err)
	}
	if got := rejected.Get("Authorization"); got != "Bearer old-token" {
		t.Fatalf("initial Authorization = %q", got)
	}
	if got := rejected.Get("ChatGPT-Account-ID"); got != "workspace-123" {
		t.Fatalf("initial ChatGPT-Account-ID = %q", got)
	}

	// Another process rotated the credential while the executor ran.
	saveExecServerChatGPTAuthForTest(t, home, map[string]any{
		"access_token": "rotated-token", "account_id": "workspace-123", "refresh_token": "refresh-token",
	})
	if !provider.RefreshAfterUnauthorized(context.Background(), rejected) {
		t.Fatal("RefreshAfterUnauthorized() = false, want the stored rotation to be reused")
	}
	if got := refreshCalls.Load(); got != 0 {
		t.Fatalf("refresh calls = %d, want 0 (the stored rotation must be reused)", got)
	}
}

// TestExecServerRegistryAuthRefreshesOnceAfterUnauthorizedLikeRust checks the
// forced refresh path and that the refreshed credential is what the replay
// would send.
func TestExecServerRegistryAuthRefreshesOnceAfterUnauthorizedLikeRust(t *testing.T) {
	home := t.TempDir()
	saveExecServerChatGPTAuthForTest(t, home, map[string]any{
		"access_token": "old-token", "account_id": "workspace-123", "refresh_token": "refresh-token",
	})
	var mu sync.Mutex
	var refreshBodies []map[string]string
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		refreshBodies = append(refreshBodies, body)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "new-token", "refresh_token": "new-refresh-token",
		})
	}))
	defer refreshServer.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, refreshServer.URL)

	provider := execServerAuthForTest(t, home, refreshServer.Client())
	rejected, err := provider.ResolveAuthHeaders(context.Background())
	if err != nil {
		t.Fatalf("ResolveAuthHeaders() error = %v", err)
	}
	if !provider.RefreshAfterUnauthorized(context.Background(), rejected) {
		t.Fatal("RefreshAfterUnauthorized() = false, want a successful refresh")
	}
	refreshed, err := provider.ResolveAuthHeaders(context.Background())
	if err != nil {
		t.Fatalf("ResolveAuthHeaders() after refresh error = %v", err)
	}
	if got := refreshed.Get("Authorization"); got != "Bearer new-token" {
		t.Fatalf("refreshed Authorization = %q", got)
	}
	if got := refreshed.Get("ChatGPT-Account-ID"); got != "workspace-123" {
		t.Fatalf("refreshed ChatGPT-Account-ID = %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(refreshBodies) != 1 {
		t.Fatalf("refresh calls = %d, want 1", len(refreshBodies))
	}
	if got := refreshBodies[0]["refresh_reason"]; got != "forced" {
		t.Fatalf("refresh_reason = %q, want forced", got)
	}
}

// TestExecServerRegistryAuthDoesNotReplayKnownFailedCredentialLikeRust checks
// that a failed refresh is not retried with the same rejected credential.
func TestExecServerRegistryAuthDoesNotReplayKnownFailedCredentialLikeRust(t *testing.T) {
	home := t.TempDir()
	saveExecServerChatGPTAuthForTest(t, home, map[string]any{
		"access_token": "old-token", "account_id": "workspace-123", "refresh_token": "refresh-token",
	})
	var refreshCalls atomic.Int32
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer refreshServer.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, refreshServer.URL)

	provider := execServerAuthForTest(t, home, refreshServer.Client())
	rejected, err := provider.ResolveAuthHeaders(context.Background())
	if err != nil {
		t.Fatalf("ResolveAuthHeaders() error = %v", err)
	}
	if provider.RefreshAfterUnauthorized(context.Background(), rejected) {
		t.Fatal("RefreshAfterUnauthorized() = true after a failed refresh")
	}
	// A late second rejection must not reuse a credential known to have failed,
	// and must not spend another refresh request on it.
	if provider.RefreshAfterUnauthorized(context.Background(), http.Header{}) {
		t.Fatal("RefreshAfterUnauthorized() = true for a known permanent failure")
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
}

// TestExecServerRegistryAuthConcurrentRefreshesShareOneRefreshLikeRust mirrors
// Rust's concurrent coverage: queued callers reuse the completed refresh
// instead of refreshing the rotated token again.
func TestExecServerRegistryAuthConcurrentRefreshesShareOneRefreshLikeRust(t *testing.T) {
	home := t.TempDir()
	saveExecServerChatGPTAuthForTest(t, home, map[string]any{
		"access_token": "old-token", "account_id": "workspace-123", "refresh_token": "refresh-token",
	})
	var refreshCalls atomic.Int32
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "new-token", "refresh_token": "new-refresh-token",
		})
	}))
	defer refreshServer.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, refreshServer.URL)

	provider := execServerAuthForTest(t, home, refreshServer.Client())
	rejected, err := provider.ResolveAuthHeaders(context.Background())
	if err != nil {
		t.Fatalf("ResolveAuthHeaders() error = %v", err)
	}
	results := make([]bool, 2)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index] = provider.RefreshAfterUnauthorized(context.Background(), rejected)
		}(index)
	}
	wait.Wait()
	for index, result := range results {
		if !result {
			t.Fatalf("concurrent RefreshAfterUnauthorized()[%d] = false, want true", index)
		}
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1 shared refresh", got)
	}
}

// TestRemoteExecServerRefreshesChatGPTAuthAndReplaysLikeRust is the CLI-level
// equivalent of Rust's auth_refresh_tests: a running executor whose ChatGPT
// credential is rejected refreshes it, reloads the store, and replays the
// registration with the replacement credential.
func TestRemoteExecServerRefreshesChatGPTAuthAndReplaysLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")

	var mu sync.Mutex
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case execServerCloudBundlePath:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		case execServerCloudRegisterPath:
			authorization := r.Header.Get("Authorization")
			mu.Lock()
			authorizations = append(authorizations, authorization)
			mu.Unlock()
			if authorization == "Bearer old-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, "registration reached")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var refreshCalls atomic.Int32
	refreshServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token": "new-token", "refresh_token": "new-refresh-token",
		})
	}))
	defer refreshServer.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, refreshServer.URL)

	body := fmt.Sprintf("cli_auth_credentials_store = \"file\"\nchatgpt_base_url = %q\n", server.URL+"/backend-api")
	configPath := config.ConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(config) error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	saveExecServerChatGPTAuthForTest(t, home, map[string]any{
		"access_token": "old-token", "account_id": "workspace-123", "refresh_token": "refresh-token",
	})

	err := Run(context.Background(), []string{
		"exec-server", "--remote", server.URL, "--environment-id", "test",
	}, strings.NewReader(""), &strings.Builder{}, &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "registration reached") {
		t.Fatalf("remote exec-server error = %v, want the post-refresh registration", err)
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"Bearer old-token", "Bearer new-token"}
	if len(authorizations) != len(want) {
		t.Fatalf("registration authorizations = %v, want %v", authorizations, want)
	}
	for index := range want {
		if authorizations[index] != want[index] {
			t.Fatalf("registration authorizations = %v, want %v", authorizations, want)
		}
	}
}
