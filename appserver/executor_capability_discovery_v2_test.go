package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/config"
	execserverclient "codex_go/execserver"
	"codex_go/session"
	"codex_go/turn"
	"codex_go/utils"

	"github.com/coder/websocket"
)

type capabilityDiscoveryV2URLWriter struct {
	once sync.Once
	url  chan<- string
}

func (w *capabilityDiscoveryV2URLWriter) Write(data []byte) (int, error) {
	value := strings.TrimSpace(string(data))
	if value != "" {
		w.once.Do(func() { w.url <- value })
	}
	return len(data), nil
}

func startCapabilityDiscoveryV2Executor(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	urlCh := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- execserverclient.NewServer().ServeTransport(ctx, "ws://127.0.0.1:0", nil, &capabilityDiscoveryV2URLWriter{url: urlCh})
	}()
	var url string
	select {
	case url = <-urlCh:
	case err := <-done:
		cancel()
		t.Fatalf("exec-server exited before publishing its URL: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out waiting for exec-server URL")
	}
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	return url
}

func TestExecutorCapabilityDiscoveryV2AcquiresInertInventoryAndReusesTurnCacheLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	skillDir := filepath.Join(codexHome, "skills", "inert")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: inert\ndescription: must stay inert\n---\nPRIVATE BODY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverURL := startCapabilityDiscoveryV2Executor(t)

	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: "thread-inventory", SessionID: "thread-inventory",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if _, err := router.requireEnvironment().Add(&EnvironmentAddParams{
		EnvironmentID: "remote",
		ExecServerURL: serverURL,
	}); err != nil {
		t.Fatal(err)
	}
	cwdURI, err := utils.FromHostNativePath(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	params := &turn.TurnStartParams{
		Environments: []map[string]any{{
			"environmentId": "remote",
			"cwd":           cwdURI.String(),
			"config":        environmentConfigStateToAny(EnvironmentConfigState{Kind: EnvironmentConfigReady, Config: &EnvironmentConfig{}}),
		}},
	}
	cfg := &config.Config{Values: map[string]any{
		"features": map[string]any{"executor_capability_discovery_v2": true},
	}}
	cache := router.threads.capabilityDiscoveryV2Cache("thread-inventory")
	first := router.executorCapabilityDiscoveryV2ForStep(context.Background(), "thread-inventory", params, cfg, nil, cache)
	if first == nil || len(*first) != 1 || (*first)[0].Err != "" || (*first)[0].Response == nil {
		t.Fatalf("first V2 inventory = %#v", first)
	}
	if len((*first)[0].Response.Skills) != 1 || (*first)[0].Response.Skills[0].Name != "inert" {
		t.Fatalf("V2 skills = %#v", (*first)[0].Response.Skills)
	}
	if strings.Contains((*first)[0].Response.Skills[0].Description, "PRIVATE") {
		t.Fatal("V2 inventory leaked skill instructions")
	}
	idleCache := router.threads.capabilityDiscoveryV2Cache("thread-inventory")
	second := router.executorCapabilityDiscoveryV2ForStep(context.Background(), "thread-inventory", params, cfg, nil, idleCache)
	if second == nil || len(*second) != 1 || (*second)[0].Response != (*first)[0].Response {
		t.Fatalf("same-turn cache = %#v, want the first response", second)
	}
	replacement := router.executorCapabilityDiscoveryV2ForStep(context.Background(), "thread-inventory", params, cfg, nil, newRuntimeCapabilityDiscoveryV2Cache())
	if replacement == nil || len(*replacement) != 1 || (*replacement)[0].Response == (*first)[0].Response {
		t.Fatalf("replacement turn cache = %#v, want a fresh inventory", replacement)
	}

	disabled := &config.Config{Values: map[string]any{}}
	if snapshots := router.executorCapabilityDiscoveryV2ForStep(context.Background(), "thread-inventory", params, disabled, nil, cache); snapshots != nil {
		t.Fatalf("disabled feature returned inventory: %#v", snapshots)
	}
}

func TestExecutorCapabilityDiscoveryV2StaysOutOfModelCatalogsLikeRust(t *testing.T) {
	snapshot := &runtimeStepCapabilitySnapshot{}
	enabled := []execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{{
		EnvironmentID: "remote",
		Response: &execserverclient.DiscoverV2CapabilitiesResponse{
			Plugins: []execserverclient.ExecutorPlugin{{ID: "inert@market"}},
			Skills:  []execserverclient.ExecutorSkill{{Name: "inert"}},
		},
	}}
	snapshot.discoveryV2 = &enabled
	if !snapshot.executorCapabilityDiscoveryV2Enabled() {
		t.Fatal("enabled empty-or-populated V2 snapshot was not reported as enabled")
	}
	cloned := snapshot.executorCapabilityDiscoveryV2()
	if len(cloned) != 1 || cloned[0].Response == enabled[0].Response {
		t.Fatalf("snapshot clone = %#v", cloned)
	}
	cloned[0].Response.Plugins[0].ID = "mutated"
	if (*snapshot.discoveryV2)[0].Response.Plugins[0].ID != "inert@market" {
		t.Fatal("consumer mutation changed the captured step inventory")
	}
	if catalog := snapshot.selectedRoots(); len(catalog) != 0 {
		t.Fatalf("inert inventory published capability roots: %#v", catalog)
	}
}

func TestExecutorCapabilityDiscoveryV2SkipsUnsupportedExecutorLikeRust(t *testing.T) {
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
			var result any
			switch call.Method {
			case "initialize":
				result = map[string]any{
					"sessionId": "unsupported-executor",
					"environmentInfo": map[string]any{
						"shell":        map[string]any{"name": "bash", "path": "/bin/bash"},
						"capabilities": map[string]any{"capabilityDiscoveryV2": false},
					},
				}
			case "capabilities/discoverV2":
				discoveryCalls.Add(1)
				result = map[string]any{"plugins": []any{}, "skills": []any{}, "warnings": []any{}}
			default:
				continue
			}
			response, _ := json.Marshal(map[string]any{"id": json.RawMessage(call.ID), "result": result})
			if err := conn.Write(context.Background(), websocket.MessageText, response); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	serverURL := "ws" + strings.TrimPrefix(server.URL, "http")

	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: "thread-unsupported", SessionID: "thread-unsupported",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir()},
	}); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
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
	snapshots := router.executorCapabilityDiscoveryV2ForStep(context.Background(), "thread-unsupported", params, cfg, nil, newRuntimeCapabilityDiscoveryV2Cache())
	if snapshots == nil || len(*snapshots) != 0 {
		t.Fatalf("unsupported executor snapshots = %#v, want enabled but empty", snapshots)
	}
	if calls := discoveryCalls.Load(); calls != 0 {
		t.Fatalf("unsupported executor discovery calls = %d, want 0", calls)
	}
}

func TestExecutorCapabilityDiscoveryV2CacheRefreshesAtTurnStartLikeRust(t *testing.T) {
	manager := NewThreadManager(nil)
	idle := manager.capabilityDiscoveryV2Cache("thread-cache")
	if idle == nil {
		t.Fatal("idle cache was not created")
	}
	if err := manager.ReserveTurn("thread-cache"); err != nil {
		t.Fatal(err)
	}
	started := manager.capabilityDiscoveryV2Cache("thread-cache")
	if started == idle {
		t.Fatal("turn start reused the previous inventory cache")
	}
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := manager.RegisterTurn("thread-cache", "turn-1", cancel, time.Now().UnixMilli(), &turn.TurnStartParams{}); err != nil {
		t.Fatal(err)
	}
	if got := manager.capabilityDiscoveryV2Cache("thread-cache"); got != started {
		t.Fatal("registering the reserved turn replaced its cache a second time")
	}
}
