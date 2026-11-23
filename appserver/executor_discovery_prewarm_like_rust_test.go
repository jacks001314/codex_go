package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"

	"github.com/coder/websocket"
)

type discoveryPrewarmExecutor struct {
	version   string
	fail      bool
	discovery atomic.Int64
}

func startDiscoveryPrewarmExecutor(t *testing.T, executor *discoveryPrewarmExecutor) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var call struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if json.Unmarshal(data, &call) != nil || len(call.ID) == 0 {
				continue
			}
			var payload map[string]any
			switch call.Method {
			case "initialize":
				payload = map[string]any{
					"jsonrpc": "2.0",
					"id":      json.RawMessage(call.ID),
					"result": map[string]any{
						"sessionId": "prewarm-executor",
						"environmentInfo": map[string]any{
							"shell":           map[string]any{"name": "bash", "path": "/bin/bash"},
							"executorVersion": executor.version,
							"capabilities":    map[string]any{"capabilityDiscoveryV2": true},
						},
					},
				}
			case "capabilities/discoverV2":
				executor.discovery.Add(1)
				if executor.fail {
					payload = map[string]any{
						"jsonrpc": "2.0",
						"id":      json.RawMessage(call.ID),
						"error":   map[string]any{"code": -32000, "message": "discovery failed"},
					}
				} else {
					payload = map[string]any{
						"jsonrpc": "2.0",
						"id":      json.RawMessage(call.ID),
						"result":  map[string]any{"plugins": []any{}, "skills": []any{}, "warnings": []any{}},
					}
				}
			default:
				continue
			}
			response, _ := json.Marshal(payload)
			if err := conn.Write(context.Background(), websocket.MessageText, response); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

type discoveryPrewarmFixture struct {
	router   *RuntimeRouter
	params   *turn.TurnStartParams
	threadID string
	cfg      *config.Config
}

func newDiscoveryPrewarmFixture(t *testing.T, serverURL string, enabled bool, active bool) *discoveryPrewarmFixture {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	threadID := "thread-prewarm"
	if err := store.Create(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	if enabled {
		if err := os.MkdirAll(filepath.Dir(config.ConfigPath(home)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config.ConfigPath(home), []byte("[features]\nexecutor_capability_discovery_v2 = true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(home), ThreadRouter: NewRouter(store)})
	t.Cleanup(func() { _ = router.Close() })
	if _, err := router.requireEnvironment().Add(&EnvironmentAddParams{
		EnvironmentID: "remote",
		ExecServerURL: serverURL,
	}); err != nil {
		t.Fatal(err)
	}
	params := &turn.TurnStartParams{
		ThreadID: threadID,
		Environments: []map[string]any{{
			"environmentId": "remote",
			"cwd":           "file:///repo",
			"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{}}),
		}},
	}
	values := map[string]any{}
	if enabled {
		values["features"] = map[string]any{"executor_capability_discovery_v2": true}
	}
	if active {
		if err := router.registerActiveRuntimeTurn(threadID, "turn-prewarm", nil, 1, params); err != nil {
			t.Fatal(err)
		}
	}
	return &discoveryPrewarmFixture{
		router:   router,
		params:   params,
		threadID: threadID,
		cfg:      &config.Config{Values: values},
	}
}

// Rust #62674: discovery started during startup is reused by the first step.
func TestExecutorDiscoveryPrewarmReusesInventoryForFirstStepLikeRust(t *testing.T) {
	executor := &discoveryPrewarmExecutor{version: "0.164.0"}
	fixture := newDiscoveryPrewarmFixture(t, startDiscoveryPrewarmExecutor(t, executor), true, true)
	cache := fixture.router.threads.capabilityDiscoveryV2Cache(fixture.threadID)
	fixture.router.prewarmExecutorCapabilityDiscoveryV2(context.Background(), fixture.threadID, fixture.params, cache)
	if got := executor.discovery.Load(); got != 1 {
		t.Fatalf("prewarm discovery calls = %d, want 1", got)
	}
	snapshots := fixture.router.executorCapabilityDiscoveryV2ForStep(context.Background(), fixture.threadID, fixture.params, fixture.cfg, nil, cache)
	if snapshots == nil || len(*snapshots) != 1 || (*snapshots)[0].Response == nil {
		t.Fatalf("step snapshots = %#v", snapshots)
	}
	if got := executor.discovery.Load(); got != 1 {
		t.Fatalf("step discovery calls = %d, want the prewarmed inventory", got)
	}
}

// Rust #62674: discovery errors leave turn startup running; the step observes
// the cached failure.
func TestExecutorDiscoveryPrewarmToleratesFailuresLikeRust(t *testing.T) {
	executor := &discoveryPrewarmExecutor{version: "0.164.0", fail: true}
	fixture := newDiscoveryPrewarmFixture(t, startDiscoveryPrewarmExecutor(t, executor), true, true)
	cache := fixture.router.threads.capabilityDiscoveryV2Cache(fixture.threadID)
	fixture.router.prewarmExecutorCapabilityDiscoveryV2(context.Background(), fixture.threadID, fixture.params, cache)
	if got := executor.discovery.Load(); got != 1 {
		t.Fatalf("prewarm discovery calls = %d, want 1", got)
	}
	snapshots := fixture.router.executorCapabilityDiscoveryV2ForStep(context.Background(), fixture.threadID, fixture.params, fixture.cfg, nil, cache)
	if snapshots == nil || len(*snapshots) != 1 || (*snapshots)[0].Err == "" {
		t.Fatalf("step snapshots = %#v, want the cached discovery failure", snapshots)
	}
	if got := executor.discovery.Load(); got != 1 {
		t.Fatalf("step discovery calls = %d, want the cooldown to suppress a retry", got)
	}
}

// Rust #57192/#62674: executors without the V2 contract keep V1 discovery.
func TestExecutorDiscoveryPrewarmSkipsUnsupportedExecutorLikeRust(t *testing.T) {
	executor := &discoveryPrewarmExecutor{version: "0.163.0"}
	fixture := newDiscoveryPrewarmFixture(t, startDiscoveryPrewarmExecutor(t, executor), true, true)
	cache := fixture.router.threads.capabilityDiscoveryV2Cache(fixture.threadID)
	fixture.router.prewarmExecutorCapabilityDiscoveryV2(context.Background(), fixture.threadID, fixture.params, cache)
	if got := executor.discovery.Load(); got != 0 {
		t.Fatalf("unsupported executor discovery calls = %d, want 0", got)
	}
}

func TestExecutorDiscoveryPrewarmHonorsFeatureGateLikeRust(t *testing.T) {
	executor := &discoveryPrewarmExecutor{version: "0.164.0"}
	fixture := newDiscoveryPrewarmFixture(t, startDiscoveryPrewarmExecutor(t, executor), false, true)
	cache := fixture.router.threads.capabilityDiscoveryV2Cache(fixture.threadID)
	fixture.router.prewarmExecutorCapabilityDiscoveryV2(context.Background(), fixture.threadID, fixture.params, cache)
	if got := executor.discovery.Load(); got != 0 {
		t.Fatalf("disabled feature discovery calls = %d, want 0", got)
	}
}

// Rust #62674: the turn-start path starts the prewarm before the runtime is
// ready, so the first step can reuse or join the fetch.
func TestTurnStartPrewarmsExecutorDiscoveryLikeRust(t *testing.T) {
	executor := &discoveryPrewarmExecutor{version: "0.164.0"}
	fixture := newDiscoveryPrewarmFixture(t, startDiscoveryPrewarmExecutor(t, executor), true, false)
	response, err := fixture.router.requireTurns().Start(&turn.TurnStartParams{ThreadID: fixture.threadID})
	if err != nil {
		t.Fatalf("Turns.Start() error = %v", err)
	}
	fixture.router.startTurnRuntimeAsync(fixture.params, response, "", nil)
	deadline := time.Now().Add(5 * time.Second)
	for executor.discovery.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("turn start did not prewarm executor discovery")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
