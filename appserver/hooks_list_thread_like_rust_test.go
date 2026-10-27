package appserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// TestRuntimeRouterHooksListThreadModeLikeRust mirrors Rust #56553: `threadId`
// lists a loaded thread's hook inventory, rejects a request that also names
// cwds, and reports an unknown thread as not found.
func TestRuntimeRouterHooksListThreadModeLikeRust(t *testing.T) {
	home := t.TempDir()
	threadCWD := t.TempDir()
	defaultCWD := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	record := &session.Record{
		ID: "thread-1", SessionID: "thread-1",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: threadCWD},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		DefaultCWD:     defaultCWD,
		Hooks:          NewHookRegistry(),
		HooksDiscovery: NewHookDiscoveryService(home),
		ThreadRouter:   NewRouter(store),
	})
	if err := router.services.Hooks.Add(threadCWD, sampleMetadata("thread-hook", 1)); err != nil {
		t.Fatalf("add thread hook: %v", err)
	}
	if err := router.services.Hooks.Add(defaultCWD, sampleMetadata("default-hook", 2)); err != nil {
		t.Fatalf("add default hook: %v", err)
	}
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("retain live thread: %v", err)
	}

	threadID := "thread-1"
	response := router.Handle(requestWithParams(t, IntID(1), MethodHooksList, HookListParams{ThreadID: &threadID}))
	if response.Error != nil {
		t.Fatalf("hooks/list threadId error = %+v", response.Error)
	}
	result := response.Result.(*HookListResponse)
	if len(result.Data) != 1 || result.Data[0].CWD != threadCWD {
		t.Fatalf("hooks/list threadId = %+v", result)
	}
	if len(result.Data[0].Hooks) != 1 || result.Data[0].Hooks[0].Key != "thread-hook" {
		t.Fatalf("hooks/list thread hooks = %+v", result.Data[0].Hooks)
	}

	response = router.Handle(requestWithParams(t, IntID(2), MethodHooksList, HookListParams{
		ThreadID: &threadID,
		CWDs:     []string{threadCWD},
	}))
	if response.Error == nil || response.Error.Message != "specify either threadId or cwds" {
		t.Fatalf("hooks/list threadId+cwds error = %+v", response.Error)
	}

	missing := "thread-missing"
	response = router.Handle(requestWithParams(t, IntID(3), MethodHooksList, HookListParams{ThreadID: &missing}))
	if response.Error == nil || response.Error.Message != "thread not found: thread-missing" {
		t.Fatalf("hooks/list missing thread error = %+v", response.Error)
	}
}

// TestHookMetadataSerializesEnvironmentIDLikeRust checks the wire contract from
// Rust #56553: `environmentId` is always present and null for host-local hooks.
func TestHookMetadataSerializesEnvironmentIDLikeRust(t *testing.T) {
	metadata := sampleMetadata("local", 0)
	data, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	raw, ok := decoded["environmentId"]
	if !ok {
		t.Fatalf("environmentId missing from %s", data)
	}
	if strings.TrimSpace(string(raw)) != "null" {
		t.Fatalf("host-local environmentId = %s, want null", raw)
	}

	environmentID := "remote-env"
	metadata.EnvironmentID = &environmentID
	data, err = json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal environment metadata: %v", err)
	}
	if !strings.Contains(string(data), `"environmentId":"remote-env"`) {
		t.Fatalf("environment metadata = %s", data)
	}
}
