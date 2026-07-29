package appserver

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/config"
	"codex_go/model"
	"codex_go/plugin"
	"codex_go/session"
	"codex_go/turn"
)

const appserverMCPHelperEnv = "GO_WANT_APPSERVER_MCP_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(appserverMCPHelperEnv) == "1" {
		runAppserverMCPPreparationHelper()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRuntimeRouterPreparesMCPAndPluginRecommendationsConcurrently(t *testing.T) {
	router, agent, sink, releaseFile, recommendationStarted, threadID := newConcurrentTurnPreparationTest(t)

	turnStart := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "use required-preparation and suggest a plugin",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	waitForConcurrentPreparationStarted(t, releaseFile, recommendationStarted)
	if err := os.WriteFile(releaseFile, []byte("ready"), 0o600); err != nil {
		t.Fatalf("release preparation: %v", err)
	}

	request := waitForRuntimeAgentRequest(t, agent)
	if input := inputItemText(request.InputItems); !strings.Contains(input, "<recommended_plugins>") || !strings.Contains(input, "GitHub") {
		t.Fatalf("recommended plugin input missing: %s", input)
	}
	if !agentRequestCodeModeHasTool(request, "mcp__required_preparation", "echo") {
		t.Fatalf("required MCP tool missing from code mode: %s", request.ClientMetadata["x-codex-turn-metadata"])
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusCompleted)
}

func TestRuntimeRouterInterruptDuringConcurrentPreparationPreventsSampling(t *testing.T) {
	router, agent, sink, releaseFile, recommendationStarted, threadID := newConcurrentTurnPreparationTest(t)

	turnStart := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "prepare then stop",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn start error: %+v", turnStart.Error)
	}
	turnID := turnStart.Result.(*turn.TurnStartResponse).Turn.ID
	waitForConcurrentPreparationStarted(t, releaseFile, recommendationStarted)
	interrupt := router.Handle(requestWithParams(t, IntID(4), MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: threadID, TurnID: turnID}))
	if interrupt.Error != nil {
		t.Fatalf("turn interrupt error: %+v", interrupt.Error)
	}
	waitForTurnCompletedStatus(t, sink, turnID, TurnStatusInterrupted)
	if err := os.WriteFile(releaseFile, []byte("ready"), 0o600); err != nil {
		t.Fatalf("release cancelled preparation: %v", err)
	}
	select {
	case request := <-agent.requests:
		t.Fatalf("cancelled preparation sampled model: %#v", request)
	case <-time.After(200 * time.Millisecond):
	}
}

func newConcurrentTurnPreparationTest(t *testing.T) (*RuntimeRouter, *recordingRuntimeAgent, *NotificationBuffer, string, <-chan struct{}, string) {
	t.Helper()
	dir := t.TempDir()
	releaseFile := filepath.Join(dir, "release")
	mcpStartedFile := releaseFile + ".mcp-started"
	recommendationStarted := make(chan struct{})
	var recommendationOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ps/plugins/suggested" {
			http.NotFound(w, r)
			return
		}
		recommendationOnce.Do(func() { close(recommendationStarted) })
		for {
			if _, err := os.Stat(releaseFile); err == nil {
				break
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true,"plugins":[{"id":"plugin_github","name":"github","status":"ENABLED","installation_policy":"AVAILABLE","release":{"display_name":"GitHub"}}]}`))
	}))
	t.Cleanup(server.Close)

	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("MkdirAll(home) error = %v", err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	configBody := `chatgpt_base_url = "` + server.URL + `"

[features]
apps = true
plugins = true
remote_plugin = true
tool_suggest = true

[mcp_servers.required-preparation]
command = ` + strconv.Quote(executable) + `
args = ["-test.run=^$"]
env = { ` + appserverMCPHelperEnv + ` = "1", APPSERVER_MCP_STARTED_FILE = ` + strconv.Quote(mcpStartedFile) + `, APPSERVER_MCP_RELEASE_FILE = ` + strconv.Quote(releaseFile) + ` }
enabled = true
startup_timeout_sec = 5
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(configBody), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store := session.NewStore(home)
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store), Config: config.NewConfigService(home), Account: auth.NewAccountManager(),
		Turns: turn.NewTurnService(), Agent: agent, Plugins: plugin.NewPluginService(),
		ThreadStatus: NewThreadStatusManager(), Models: model.NewModelService(nil),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(sink)
	plan := "pro"
	login := router.Handle(requestWithParams(t, IntID(1), MethodLoginAccount, auth.LoginAccountParams{
		Type: "chatgptAuthTokens", AccessToken: "access-token", ChatGPTAccountID: "workspace-1", ChatGPTPlanType: &plan,
	}))
	if login.Error != nil {
		t.Fatalf("login error: %+v", login.Error)
	}
	threadStart := router.Handle(requestWithParams(t, IntID(2), MethodThreadStart, ThreadStartParams{CWD: dir, Prompt: "hello"}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	return router, agent, sink, releaseFile, recommendationStarted, threadID
}

func waitForConcurrentPreparationStarted(t *testing.T, releaseFile string, recommendationStarted <-chan struct{}) {
	t.Helper()
	select {
	case <-recommendationStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("recommended plugin request did not start")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(releaseFile + ".mcp-started"); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("required MCP startup did not overlap the blocked recommendation request")
}

func runAppserverMCPPreparationHelper() {
	_ = os.WriteFile(os.Getenv("APPSERVER_MCP_STARTED_FILE"), []byte("started"), 0o600)
	for {
		if _, err := os.Stat(os.Getenv("APPSERVER_MCP_RELEASE_FILE")); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.ID) == 0 {
			continue
		}
		result := any(map[string]any{})
		switch request.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "preparation", "version": "test"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{"name": "echo", "description": "Echo arguments", "inputSchema": map[string]any{"type": "object"}}}}
		case "resources/list":
			result = map[string]any{"resources": []any{}}
		case "resources/templates/list":
			result = map[string]any{"resourceTemplates": []any{}}
		}
		_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}
}
