package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type emptyRemoteSkillWatchFixture struct {
	t            *testing.T
	supportWatch bool
	// emptyFirstWalk makes the first fs/walk return no entries and every later
	// walk return the skill, so a cache miss is observable through fs/walk.
	emptyFirstWalk bool
	rootURI        string
	skillURI       string
	skillBody      string

	mu         sync.Mutex
	initials   int
	walks      int
	watchCalls int
	writeMu    sync.Mutex
	watchReady chan struct{}
	watchOnce  sync.Once
	change     chan struct{}
}

func newEmptyRemoteSkillWatchFixture(t *testing.T, rootURI string, skillURI string, skillBody string, emptyFirstWalk bool, supportWatch bool) *emptyRemoteSkillWatchFixture {
	t.Helper()
	return &emptyRemoteSkillWatchFixture{
		t:              t,
		supportWatch:   supportWatch,
		emptyFirstWalk: emptyFirstWalk,
		rootURI:        rootURI,
		skillURI:       skillURI,
		skillBody:      skillBody,
		watchReady:     make(chan struct{}),
		change:         make(chan struct{}),
	}
}

func (f *emptyRemoteSkillWatchFixture) serve(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	for {
		_, data, err := conn.Read(context.Background())
		if err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.Unmarshal(data, &request); err != nil || len(request.ID) == 0 {
			continue
		}
		result, rpcErr := f.result(conn, request.Method, request.Params)
		message := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID)}
		if rpcErr != "" {
			message["error"] = map[string]any{"code": -32601, "message": rpcErr}
		} else {
			message["result"] = result
		}
		if err := f.write(conn, message); err != nil {
			return
		}
	}
}

func (f *emptyRemoteSkillWatchFixture) result(conn *websocket.Conn, method string, params map[string]any) (any, string) {
	switch method {
	case "initialize":
		f.mu.Lock()
		f.initials++
		f.mu.Unlock()
		return map[string]any{
			"sessionId": "empty-remote-skills",
			"environmentInfo": map[string]any{
				"shell":           map[string]any{"name": "bash", "path": "/bin/bash"},
				"executorVersion": "0.0.0",
				"cwd":             nil,
				"capabilities": map[string]any{
					"fsWatchMissing": f.supportWatch,
				},
			},
		}, ""
	case "fs/walk":
		f.mu.Lock()
		f.walks++
		walk := f.walks
		f.mu.Unlock()
		if params["path"] != f.rootURI {
			return nil, fmt.Sprintf("fs/walk path = %v, want %s", params["path"], f.rootURI)
		}
		entries := make([]any, 0, 1)
		if walk > 1 || !f.emptyFirstWalk {
			entries = append(entries, map[string]any{"path": f.skillURI, "kind": "file"})
		}
		return map[string]any{"entries": entries, "errors": []any{}, "truncated": false}, ""
	case "fs/getMetadata":
		path, _ := params["path"].(string)
		return map[string]any{"isFile": path == f.skillURI, "isDirectory": false, "isSymlink": false}, ""
	case "fs/readFile":
		path, _ := params["path"].(string)
		if path != f.skillURI {
			return nil, fmt.Sprintf("fs/readFile path = %v, want %s", params["path"], f.skillURI)
		}
		return map[string]any{"dataBase64": base64.StdEncoding.EncodeToString([]byte(f.skillBody))}, ""
	case "fs/watchMissing":
		f.mu.Lock()
		f.watchCalls++
		f.mu.Unlock()
		if !f.supportWatch {
			return map[string]any{"watching": false}, ""
		}
		watchID := params["watchId"]
		f.watchOnce.Do(func() { close(f.watchReady) })
		go func() {
			<-f.change
			_ = f.write(conn, map[string]any{
				"jsonrpc": "2.0",
				"method":  "fs/missingChanged",
				"params":  map[string]any{"watchId": watchID},
			})
		}()
		return map[string]any{"watching": true}, ""
	default:
		return nil, "unsupported method " + method
	}
}

func (f *emptyRemoteSkillWatchFixture) write(conn *websocket.Conn, message map[string]any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	return conn.Write(context.Background(), websocket.MessageText, data)
}

func (f *emptyRemoteSkillWatchFixture) snapshot() (initials int, walks int, watchCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.initials, f.walks, f.watchCalls
}

func (f *emptyRemoteSkillWatchFixture) waitFor(t *testing.T, message string, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", message)
}

func newEmptyRemoteSkillWatchRouter(t *testing.T, fixture *emptyRemoteSkillWatchFixture) (*RuntimeRouter, *EnvironmentRecord, SelectedCapabilityRoot) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	environment := NewEnvironmentManager(EnvironmentShellInfo{Name: "bash", Path: "/bin/bash"}, "")
	if _, err := environment.Add(&EnvironmentAddParams{
		EnvironmentID: "remote",
		ExecServerURL: "ws" + strings.TrimPrefix(server.URL, "http"),
	}); err != nil {
		t.Fatalf("environment Add() error = %v", err)
	}
	record, ok := environment.Record("remote")
	if !ok {
		t.Fatal("environment record is missing")
	}
	router := NewRuntimeRouter(RuntimeServices{Environment: environment})
	t.Cleanup(router.stopEmptyRemoteSkillWatches)
	selected := SelectedCapabilityRoot{
		ID: "remote-cap-root",
		Location: CapabilityRootLocation{
			Type:          CapabilityRootLocationEnvironment,
			EnvironmentID: "remote",
			Path:          fixture.rootURI,
		},
	}
	return router, record, selected
}

func TestEmptyRemoteSkillCatalogWatchesMissingRootLikeRust(t *testing.T) {
	fixture := newEmptyRemoteSkillWatchFixture(t, "file:///remote/empty-skills", "file:///remote/empty-skills/watched-skill/SKILL.md", "---\nname: watched-skill\ndescription: Created after the watch.\n---\n", true, true)
	router, record, selected := newEmptyRemoteSkillWatchRouter(t, fixture)
	first := router.selectedCapabilitySkillCatalog("thread-watch", "root-key")

	entries, warnings, partial := router.remoteSelectedSkillCatalog(context.Background(), "thread-watch", "root-key", selected, record, nil)
	if len(entries) != 0 || len(warnings) != 0 || partial {
		t.Fatalf("first discovery = entries %#v warnings %#v partial %v, want a clean empty catalog", entries, warnings, partial)
	}
	fixture.waitFor(t, "fs/watchMissing registration", func() bool {
		_, _, watchCalls := fixture.snapshot()
		return watchCalls == 1
	})
	close(fixture.change)
	fixture.waitFor(t, "empty catalog invalidation", func() bool {
		return router.selectedCapabilitySkillCatalog("thread-watch", "root-key") != first
	})

	entries, warnings, partial = router.remoteSelectedSkillCatalog(context.Background(), "thread-watch", "root-key", selected, record, nil)
	if len(entries) != 1 || entries[0].Name != "watched-skill" || len(warnings) != 0 || partial {
		t.Fatalf("rescanned catalog = entries %#v warnings %#v partial %v", entries, warnings, partial)
	}
	_, walks, _ := fixture.snapshot()
	if walks != 2 {
		t.Fatalf("fs/walk calls = %d, want a second walk after invalidation", walks)
	}
}

func TestNonEmptyRemoteSkillCatalogDoesNotWatchLikeRust(t *testing.T) {
	fixture := newEmptyRemoteSkillWatchFixture(t, "file:///remote/present-skills", "file:///remote/present-skills/present-skill/SKILL.md", "---\nname: present-skill\ndescription: Already present.\n---\n", false, true)
	router, record, selected := newEmptyRemoteSkillWatchRouter(t, fixture)

	entries, warnings, partial := router.remoteSelectedSkillCatalog(context.Background(), "thread-present", "root-key", selected, record, nil)
	if len(entries) != 1 || entries[0].Name != "present-skill" || len(warnings) != 0 || partial {
		t.Fatalf("catalog = entries %#v warnings %#v partial %v", entries, warnings, partial)
	}
	initials, walks, watchCalls := fixture.snapshot()
	if initials != 1 || walks != 1 || watchCalls != 0 {
		t.Fatalf("executor calls = initials %d walks %d watchMissing %d, want one ordinary discovery and no watch", initials, walks, watchCalls)
	}
}

func TestUnsupportedRemoteSkillWatchKeepsCatalogCacheLikeRust(t *testing.T) {
	fixture := newEmptyRemoteSkillWatchFixture(t, "file:///remote/unsupported-skills", "file:///remote/unsupported-skills/later-skill/SKILL.md", "---\nname: later-skill\ndescription: Not observed without fsWatchMissing.\n---\n", true, false)
	router, record, selected := newEmptyRemoteSkillWatchRouter(t, fixture)

	entries, warnings, partial := router.remoteSelectedSkillCatalog(context.Background(), "thread-unsupported", "root-key", selected, record, nil)
	if len(entries) != 0 || len(warnings) != 0 || partial {
		t.Fatalf("first discovery = entries %#v warnings %#v partial %v", entries, warnings, partial)
	}
	fixture.waitFor(t, "the watch client to observe missing executor capabilities", func() bool {
		initials, _, _ := fixture.snapshot()
		return initials >= 2
	})
	if _, _, watchCalls := fixture.snapshot(); watchCalls != 0 {
		t.Fatalf("fs/watchMissing calls = %d, want none without the capability", watchCalls)
	}

	entries, warnings, partial = router.remoteSelectedSkillCatalog(context.Background(), "thread-unsupported", "root-key", selected, record, nil)
	if len(entries) != 0 || len(warnings) != 0 || partial {
		t.Fatalf("cached catalog = entries %#v warnings %#v partial %v", entries, warnings, partial)
	}
	_, walks, _ := fixture.snapshot()
	if walks != 1 {
		t.Fatalf("fs/walk calls = %d, want the ordinary empty catalog cache", walks)
	}
}
