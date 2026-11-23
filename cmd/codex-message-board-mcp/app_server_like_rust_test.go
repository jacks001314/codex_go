package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/appserver"
	"codex_go/session"
	"codex_go/state"
)

type fakeBoardInject struct {
	connection int
	threadID   string
	item       map[string]any
}

type fakeBoardAppServer struct {
	t               *testing.T
	server          *httptest.Server
	mu              sync.Mutex
	connections     int
	injects         []fakeBoardInject
	hangConnections int
}

func newFakeBoardAppServer(t *testing.T, hangConnections int) *fakeBoardAppServer {
	t.Helper()
	fake := &fakeBoardAppServer{t: t, hangConnections: hangConnections}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.handle))
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeBoardAppServer) endpoint() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http")
}

func (f *fakeBoardAppServer) capturedInjects() []fakeBoardInject {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeBoardInject(nil), f.injects...)
}

func replyBoardRPC(writeMu *sync.Mutex, conn *websocket.Conn, id json.RawMessage, result any) {
	if len(id) == 0 {
		return
	}
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "result": result})
	if err != nil {
		return
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	_ = conn.Write(context.Background(), websocket.MessageText, payload)
}

func (f *fakeBoardAppServer) handle(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	f.mu.Lock()
	f.connections++
	connection := f.connections
	f.mu.Unlock()
	var writeMu sync.Mutex
	for {
		_, data, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var message struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(data, &message); err != nil {
			return
		}
		switch message.Method {
		case "initialize", "initialized":
			replyBoardRPC(&writeMu, conn, message.ID, map[string]any{})
		case "thread/inject_items":
			var params struct {
				ThreadID string            `json:"threadId"`
				Items    []json.RawMessage `json:"items"`
			}
			_ = json.Unmarshal(message.Params, &params)
			item := map[string]any{}
			if len(params.Items) > 0 {
				_ = json.Unmarshal(params.Items[0], &item)
			}
			f.mu.Lock()
			f.injects = append(f.injects, fakeBoardInject{connection: connection, threadID: params.ThreadID, item: item})
			hang := connection <= f.hangConnections
			f.mu.Unlock()
			if hang {
				continue
			}
			replyBoardRPC(&writeMu, conn, message.ID, map[string]any{})
		}
	}
}

// Rust #63354: notices are queued through thread/inject_items without starting
// a turn, concurrent notices share one connection, and the payload carries the
// attributed board text as an agent message.
func TestMessageBoardAppServerDeliversNoticesLikeRust(t *testing.T) {
	fake := newFakeBoardAppServer(t, 0)
	server, err := newMessageBoardAppServer(context.Background(), fake.endpoint())
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	const concurrent = 8
	var wait sync.WaitGroup
	errs := make(chan error, concurrent)
	for index := 0; index < concurrent; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errs <- server.inject(context.Background(), "child-thread", messageBoardNotice{
				author: "/root", recipient: "/root/worker",
				content: fmt.Sprintf("Message Type: CHANNEL_POST\nPayload:\nnotice %d", index),
			})
		}(index)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent inject error = %v", err)
		}
	}
	injects := fake.capturedInjects()
	if len(injects) != concurrent {
		t.Fatalf("inject requests = %d, want %d", len(injects), concurrent)
	}
	for _, inject := range injects {
		if inject.connection != 1 {
			t.Fatalf("notice used connection %d, want the shared connection", inject.connection)
		}
		if inject.threadID != "child-thread" {
			t.Fatalf("threadId = %q", inject.threadID)
		}
		if inject.item["type"] != "agent_message" || inject.item["author"] != "/root" || inject.item["recipient"] != "/root/worker" {
			t.Fatalf("notice item = %#v", inject.item)
		}
		blocks, ok := inject.item["content"].([]any)
		if !ok || len(blocks) != 1 {
			t.Fatalf("notice content = %#v", inject.item["content"])
		}
		block, _ := blocks[0].(map[string]any)
		if block["type"] != "input_text" || !strings.Contains(fmt.Sprint(block["text"]), "CHANNEL_POST") {
			t.Fatalf("notice block = %#v", block)
		}
	}
}

// Rust #63354: a notice over the shared context budget is rejected before it
// reaches the app-server.
func TestMessageBoardAppServerRejectsOversizedNoticeLikeRust(t *testing.T) {
	fake := newFakeBoardAppServer(t, 0)
	server, err := newMessageBoardAppServer(context.Background(), fake.endpoint())
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	err = server.inject(context.Background(), "child-thread", messageBoardNotice{
		author: "/root", recipient: "/root/worker", content: strings.Repeat("x", messageBoardNoticeLimit),
	})
	if err == nil || !strings.Contains(err.Error(), "context budget") {
		t.Fatalf("oversized notice error = %v", err)
	}
	if injects := fake.capturedInjects(); len(injects) != 0 {
		t.Fatalf("oversized notice reached the app-server: %#v", injects)
	}
}

// Rust #63354: a notice that times out drops the transport instead of replaying,
// and the next notice reconnects and succeeds with a fresh request.
func TestMessageBoardAppServerReconnectsAfterTimeoutWithoutReplayLikeRust(t *testing.T) {
	fake := newFakeBoardAppServer(t, 1)
	previous := messageBoardAppServerRequestTimeout
	messageBoardAppServerRequestTimeout = 300 * time.Millisecond
	t.Cleanup(func() { messageBoardAppServerRequestTimeout = previous })

	server, err := newMessageBoardAppServer(context.Background(), fake.endpoint())
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	err = server.inject(context.Background(), "child-thread", messageBoardNotice{
		author: "/root", recipient: "/root/worker", content: "failed notice",
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timed out notice error = %v", err)
	}
	if err := server.inject(context.Background(), "child-thread", messageBoardNotice{
		author: "/root", recipient: "/root/worker", content: "second notice",
	}); err != nil {
		t.Fatalf("second notice error = %v", err)
	}
	injects := fake.capturedInjects()
	if len(injects) != 2 {
		t.Fatalf("inject requests = %d, want the failed notice and the retry", len(injects))
	}
	second := injects[1]
	if second.connection == injects[0].connection {
		t.Fatal("second notice reused the timed out connection")
	}
	texts := fmt.Sprint(second.item["content"])
	if !strings.Contains(texts, "second notice") || strings.Contains(texts, "failed notice") {
		t.Fatalf("second notice replayed the failed one: %s", texts)
	}
}

// Rust #63354: the registry host delivers a board notice to the recipient's
// mailbox and reports the accepted delivery.
func TestRegistryBoardHostDeliversNoticeThroughAppServerLikeRust(t *testing.T) {
	fake := newFakeBoardAppServer(t, 0)
	server, err := newMessageBoardAppServer(context.Background(), fake.endpoint())
	if err != nil {
		t.Fatalf("connect error: %v", err)
	}
	registry := newTestBoardRegistry(t, map[string]string{"child-thread": "/root/worker"})
	t.Cleanup(func() { _ = registry.Close() })
	host := &registryBoardHost{registry: registry, now: time.Now, appServer: server}
	post := agentboard.PostPreview{
		PostMetadata: agentboard.PostMetadata{
			Author: "/root", ChannelName: "general", MessageID: "message-1", ThreadID: "thread-1",
		},
		TextPreview: "run the tests",
	}
	delivery, err := host.Notify(context.Background(), "child-thread", post)
	if err != nil || delivery != agentboard.NotificationAccepted {
		t.Fatalf("delivery = %q err = %v", delivery, err)
	}
	injects := fake.capturedInjects()
	if len(injects) != 1 || injects[0].threadID != "child-thread" {
		t.Fatalf("injects = %#v", injects)
	}
	text := fmt.Sprint(injects[0].item["content"])
	if !strings.Contains(text, "Message Type: CHANNEL_POST") || !strings.Contains(text, "run the tests") {
		t.Fatalf("notice text = %s", text)
	}

	if _, err := host.Notify(context.Background(), "missing-thread", post); err == nil {
		t.Fatal("unknown recipient did not report an error")
	}
	skipped := &registryBoardHost{registry: registry, now: time.Now}
	if delivery, err := skipped.Notify(context.Background(), "child-thread", post); err != nil || delivery != agentboard.NotificationSkippedInactive {
		t.Fatalf("unconfigured delivery = %q err = %v", delivery, err)
	}
}

func newTestBoardRegistry(t *testing.T, threads map[string]string) *messageBoardMCPRegistry {
	t.Helper()
	home := t.TempDir()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatalf("sqlite config: %v", err)
	}
	path := sqliteConfig.StateDBPath()
	db, openErr := state.OpenSQLite(context.Background(), "file:"+path)
	if openErr != nil {
		t.Fatalf("open state db: %v", openErr)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, agent_path TEXT, updated_at INTEGER)`); err != nil {
		t.Fatalf("create threads: %v", err)
	}
	for threadID, path := range threads {
		if _, err := db.Exec(`INSERT INTO threads (id, agent_path, updated_at) VALUES (?, ?, 1)`, threadID, path); err != nil {
			t.Fatalf("insert thread: %v", err)
		}
	}
	registry, err := openMessageBoardMCPRegistry(context.Background(), path)
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	return registry
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Rust #63354: a notice delivered through a real app-server reaches the native
// thread's persisted history without starting a turn.
func TestMessageBoardNoticeLandsInNativeHistoryLikeRust(t *testing.T) {
	// The app-server keeps a thread writer lock open until the process exits, so
	// the temporary home is removed best-effort instead of by t.TempDir.
	home, err := os.MkdirTemp("", "message-board-e2e")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	storeRoot := filepath.Join(home, "sessions")
	store := session.NewStore(storeRoot)
	ready := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- appserver.ServeWebSocket(ctx, &appserver.WebSocketOptions{
			CodexHome: home,
			StoreRoot: storeRoot,
			Listen:    "ws://127.0.0.1:0",
			Ready:     ready,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-served:
		case <-time.After(10 * time.Second):
			t.Error("app-server did not stop")
		}
	})
	var endpoint string
	deadline := time.Now().Add(10 * time.Second)
	for endpoint == "" {
		if fields := strings.Fields(ready.String()); len(fields) >= 5 && strings.HasPrefix(fields[len(fields)-1], "ws://") {
			endpoint = strings.TrimSpace(fields[len(fields)-1])
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("app-server did not become ready: %q", ready.String())
		}
		time.Sleep(20 * time.Millisecond)
	}

	server, err := newMessageBoardAppServer(context.Background(), endpoint)
	if err != nil {
		t.Fatalf("connect error = %v", err)
	}
	startRaw, err := server.request(context.Background(), string(appserver.MethodThreadStart), appserver.ThreadStartParams{CWD: home})
	if err != nil {
		t.Fatalf("thread/start error = %v", err)
	}
	var started appserver.ThreadStartResponse
	if err := json.Unmarshal(startRaw, &started); err != nil {
		t.Fatalf("decode thread/start: %v", err)
	}
	if started.Thread == nil || strings.TrimSpace(started.Thread.ID) == "" {
		t.Fatalf("thread/start response = %s", startRaw)
	}
	threadID := started.Thread.ID
	post := agentboard.PostPreview{
		PostMetadata: agentboard.PostMetadata{
			Author: "/root/worker", ChannelName: "general", MessageID: "message-e2e", ThreadID: "thread-e2e",
		},
		TextPreview: "native notice body",
	}
	notice := messageBoardNotice{author: string(post.Author), recipient: "/root", content: post.NotificationText()}
	if err := server.inject(ctx, threadID, notice); err != nil {
		t.Fatalf("inject error = %v", err)
	}
	after, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read session: %v", err)
	}
	rendered, err := json.Marshal(after.Items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	if !strings.Contains(string(rendered), "native notice body") {
		t.Fatalf("notice missing from history: %s", rendered)
	}
	if err := agent.ValidateAgentPath("/root"); err != nil {
		t.Fatalf("agent path fixture: %v", err)
	}
}
