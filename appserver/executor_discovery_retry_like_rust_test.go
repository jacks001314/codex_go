package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/session"
	"codex_go/turn"

	"github.com/coder/websocket"
)

func TestExecutorCapabilityDiscoveryV2SnapshotsDetectInventoryChangesLikeRust(t *testing.T) {
	cache := newRuntimeCapabilityDiscoveryV2Cache()
	if cache.recordDiscoverySnapshots(nil) {
		t.Fatal("empty first discovery reported a change")
	}
	success := execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{
		EnvironmentID: "remote",
		Response:      &execserverclient.DiscoverV2CapabilitiesResponse{},
	}
	if !cache.recordDiscoverySnapshots([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{success}) {
		t.Fatal("first non-empty discovery did not report a change")
	}
	if cache.recordDiscoverySnapshots([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{success}) {
		t.Fatal("a cache hit reported a change")
	}
	reloaded := success
	reloaded.Response = &execserverclient.DiscoverV2CapabilitiesResponse{}
	if !cache.recordDiscoverySnapshots([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{reloaded}) {
		t.Fatal("a reloaded inventory did not report a change")
	}
	failed := execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{EnvironmentID: "remote", Err: "executor discovery V2 failed"}
	if !cache.recordDiscoverySnapshots([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{failed}) {
		t.Fatal("success to failure did not report a change")
	}
	if cache.recordDiscoverySnapshots([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{failed}) {
		t.Fatal("repeated failure reported a change")
	}
	recovered := execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{EnvironmentID: "remote", Response: &execserverclient.DiscoverV2CapabilitiesResponse{}}
	if !cache.recordDiscoverySnapshots([]execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{recovered}) {
		t.Fatal("recovery did not report a change")
	}
	if !cache.clearDiscoverySnapshots() {
		t.Fatal("clearing a stored inventory did not report a change")
	}
	if cache.clearDiscoverySnapshots() {
		t.Fatal("clearing an empty inventory reported a change")
	}
}

func TestExecutorCapabilityDiscoveryV2RecoveryInvalidatesMCPLikeRust(t *testing.T) {
	var failDiscovery atomic.Bool
	var discoveryCalls atomic.Int64
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
						"sessionId": "recovery-executor",
						"environmentInfo": map[string]any{
							"shell":           map[string]any{"name": "bash", "path": "/bin/bash"},
							"executorVersion": "0.164.0",
							"capabilities":    map[string]any{"capabilityDiscoveryV2": true},
						},
					},
				}
			case "capabilities/discoverV2":
				discoveryCalls.Add(1)
				if failDiscovery.Load() {
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
	serverURL := "ws" + strings.TrimPrefix(server.URL, "http")

	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	threadID := "thread-discovery-recovery"
	if err := store.Create(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()
	if _, err := router.requireEnvironment().Add(&EnvironmentAddParams{
		EnvironmentID: "remote",
		ExecServerURL: serverURL,
	}); err != nil {
		t.Fatal(err)
	}
	params := &turn.TurnStartParams{Environments: []map[string]any{{
		"environmentId": "remote",
		"cwd":           "file:///repo",
		"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{}}),
	}}}
	cfg := &config.Config{Values: map[string]any{
		"features": map[string]any{"executor_capability_discovery_v2": true},
	}}
	cache := router.threads.capabilityDiscoveryV2Cache(threadID)
	epoch := func() uint64 {
		router.mcpRuntimes.mu.Lock()
		defer router.mcpRuntimes.mu.Unlock()
		return router.mcpRuntimes.threadEpoch[threadID]
	}
	discover := func() []execserverclient.ExecutorCapabilityDiscoveryV2Snapshot {
		snapshots := router.executorCapabilityDiscoveryV2ForStep(context.Background(), threadID, params, cfg, nil, cache)
		if snapshots == nil || len(*snapshots) != 1 {
			t.Fatalf("snapshots = %#v", snapshots)
		}
		return *snapshots
	}

	first := discover()
	if first[0].Response == nil {
		t.Fatalf("first inventory = %#v", first[0])
	}
	if got := epoch(); got != 1 {
		t.Fatalf("first observation epoch = %d, want one MCP invalidation", got)
	}
	if repeated := discover(); repeated[0].Response != first[0].Response {
		t.Fatal("cache hit did not reuse the first inventory")
	}
	if got := epoch(); got != 1 {
		t.Fatalf("cache hit epoch = %d, want no MCP invalidation", got)
	}

	failDiscovery.Store(true)
	router.threads.InvalidateCapabilityDiscoveryV2Cache(threadID)
	failed := discover()
	if failed[0].Err == "" {
		t.Fatalf("failure snapshot = %#v", failed[0])
	}
	if got := epoch(); got != 2 {
		t.Fatalf("failure epoch = %d, want an MCP invalidation", got)
	}

	failDiscovery.Store(false)
	router.threads.InvalidateCapabilityDiscoveryV2Cache(threadID)
	recovered := discover()
	if recovered[0].Response == nil || recovered[0].Response == first[0].Response {
		t.Fatalf("recovered inventory = %#v, want a fresh response", recovered[0])
	}
	if got := epoch(); got != 3 {
		t.Fatalf("recovery epoch = %d, want an MCP invalidation", got)
	}
	if got := discoveryCalls.Load(); got != 3 {
		t.Fatalf("discovery calls = %d, want success, failure and recovery", got)
	}
}
