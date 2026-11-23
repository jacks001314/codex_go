package appserver

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/session"
)

// Mirrors Rust #62292/#62537: plaintext agent mail injected into a native child
// thread is queued for the child's mailbox instead of being written into its
// history, while the same batch injected into the root keeps the persisted
// history-injection path.
func TestThreadInjectItemsRoutesChildAgentMailThroughMailboxLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	now := time.Now().UTC()
	store := session.NewStore(filepath.Join(home, "sessions"))
	root := &session.Record{
		ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: cwd, Model: "gpt-mail-test", Source: string(SessionSourceCli), AgentPath: "/root"},
	}
	child := &session.Record{
		ID: "child", SessionID: "root", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: cwd, Model: "gpt-mail-test", Source: string(SessionSourceAppServer), AgentPath: "/root/worker", AgentDepth: 1},
	}
	for _, record := range []*session.Record{root, child} {
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		DefaultCWD:   cwd,
	})
	for _, threadID := range []string{"root", "child"} {
		router.requireThreadStatus().UpsertThread(threadID, false)
	}
	mail := func(author, recipient, text string) json.RawMessage {
		raw, err := json.Marshal(map[string]any{
			"type": "agent_message", "author": author, "recipient": recipient,
			"content": []any{map[string]any{"type": "input_text", "text": text}},
		})
		if err != nil {
			t.Fatalf("Marshal(agent_message) error = %v", err)
		}
		return raw
	}

	childBefore, err := store.Read("child", true, true)
	if err != nil {
		t.Fatalf("Read(child) error = %v", err)
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: "child", Items: []json.RawMessage{mail("/root", "/root/worker", "run the tests")},
	}))
	if response.Error != nil {
		t.Fatalf("child injection = %+v", response.Error)
	}
	queued := router.drainRuntimeAgentMessages("child")
	if len(queued) != 1 {
		t.Fatalf("queued child mail = %#v, want one item", queued)
	}
	item, ok := queued[0].(map[string]any)
	if !ok || item["type"] != "agent_message" {
		t.Fatalf("queued child mail shape = %#v", queued[0])
	}
	childAfter, err := store.Read("child", true, true)
	if err != nil {
		t.Fatalf("Read(child) error = %v", err)
	}
	if len(childAfter.Items) != len(childBefore.Items) {
		t.Fatalf("routed child mail changed history: %d -> %d items", len(childBefore.Items), len(childAfter.Items))
	}

	rootBefore, err := store.Read("root", true, true)
	if err != nil {
		t.Fatalf("Read(root) error = %v", err)
	}
	response = router.Handle(requestWithParams(t, IntID(2), MethodThreadInjectItems, ThreadInjectItemsParams{
		ThreadID: "root", Items: []json.RawMessage{mail("/root/worker", "/root", "report ready")},
	}))
	if response.Error != nil {
		t.Fatalf("root injection = %+v", response.Error)
	}
	if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 0 {
		t.Fatalf("root mail was queued instead of persisted: %#v", queued)
	}
	rootAfter, err := store.Read("root", true, true)
	if err != nil {
		t.Fatalf("Read(root) error = %v", err)
	}
	if len(rootAfter.Items) != len(rootBefore.Items)+1 {
		t.Fatalf("root injection items = %d, want %d", len(rootAfter.Items), len(rootBefore.Items)+1)
	}
}

// Mirrors Rust's mixed-batch rule: anything that is not entirely valid
// plaintext agent mail keeps the raw history-injection path.
func TestThreadInjectItemsKeepsMixedBatchesOnHistoryPathLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	now := time.Now().UTC()
	store := session.NewStore(filepath.Join(home, "sessions"))
	child := &session.Record{
		ID: "child", SessionID: "root", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: cwd, Model: "gpt-mail-test", Source: string(SessionSourceAppServer), AgentPath: "/root/worker", AgentDepth: 1},
	}
	if err := store.Create(child); err != nil {
		t.Fatalf("Create(child) error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		DefaultCWD:   cwd,
	})
	router.requireThreadStatus().UpsertThread("child", false)
	before, err := store.Read("child", true, true)
	if err != nil {
		t.Fatalf("Read(child) error = %v", err)
	}
	mixed := []json.RawMessage{
		json.RawMessage(`{"type":"agent_message","author":"/root","recipient":"/root/worker","content":[{"type":"input_text","text":"one"}]}`),
		json.RawMessage(`{"type":"message","role":"user","content":[{"type":"input_text","text":"two"}]}`),
	}
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadInjectItems, ThreadInjectItemsParams{ThreadID: "child", Items: mixed}))
	if response.Error != nil {
		t.Fatalf("mixed injection = %+v", response.Error)
	}
	if queued := router.drainRuntimeAgentMessages("child"); len(queued) != 0 {
		t.Fatalf("mixed batch was routed to the mailbox: %#v", queued)
	}
	after, err := store.Read("child", true, true)
	if err != nil {
		t.Fatalf("Read(child) error = %v", err)
	}
	if len(after.Items) != len(before.Items)+2 {
		t.Fatalf("mixed injection items = %d, want %d", len(after.Items), len(before.Items)+2)
	}
}
