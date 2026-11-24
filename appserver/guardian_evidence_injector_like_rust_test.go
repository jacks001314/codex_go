package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/agentboard"
	"codex_go/session"
)

func newGuardianEvidenceInjectionFixture(t *testing.T) (*RuntimeRouter, *session.Store, string) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, DefaultCWD: t.TempDir()})
	router.rememberConnectionClientInfo("default", ClientInfo{Name: "test", Version: "1"})
	started := threadRouter.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{CWD: t.TempDir()}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %#v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID
	router.markResponseThreadLoaded(started.Result, "default")
	return router, store, threadID
}

// Rust #62136 Conversation::deliver records an admitted batch in the retained
// reviewer thread as a function-call output and never starts a turn.
func TestGuardianThreadEvidenceInjectorRecordsBatchLikeRust(t *testing.T) {
	router, store, threadID := newGuardianEvidenceInjectionFixture(t)
	injector, err := NewGuardianThreadEvidenceInjector(router, threadID)
	if err != nil {
		t.Fatalf("NewGuardianThreadEvidenceInjector() error = %v", err)
	}
	if injector.ThreadID() != threadID {
		t.Fatalf("thread id = %q, want %q", injector.ThreadID(), threadID)
	}
	item := agentboard.GuardianEvidenceItem{
		Name:      agentboard.GuardianEvidenceContextName,
		Namespace: agentboard.GuardianEvidenceNamespace,
		Text:      `{"sources":[{"id":{"ordinal":0},"original":{"type":"response_item"}}]}`,
	}
	if err := agentboard.GuardianInboxAdmit(injector)(context.Background(), item); err != nil {
		t.Fatalf("inject error = %v", err)
	}
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if len(record.Items) == 0 {
		t.Fatalf("thread recorded no injected item")
	}
	last := record.Items[len(record.Items)-1]
	if last.Type != "function_call_output" {
		t.Fatalf("injected item type = %q, want function_call_output", last.Type)
	}
	if last.Name != agentboard.GuardianEvidenceContextName || last.Namespace != agentboard.GuardianEvidenceNamespace {
		t.Fatalf("injected item name/namespace = %q/%q", last.Name, last.Namespace)
	}
	output, _ := last.Data["output"].(string)
	if !strings.Contains(output, `"ordinal":0`) {
		t.Fatalf("injected output = %#v, want the evidence batch", last.Data["output"])
	}
	if last.CallID != "" {
		t.Fatalf("injected item call id = %q, want none", last.CallID)
	}

	// An empty batch is a no-op, so a drain with nothing to record cannot move
	// history.
	before := len(record.Items)
	if err := injector.InjectResponseItems(context.Background(), nil); err != nil {
		t.Fatalf("empty inject error = %v", err)
	}
	after, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("read thread: %v", err)
	}
	if len(after.Items) != before {
		t.Fatalf("empty inject changed history: %d -> %d items", before, len(after.Items))
	}
}

// A thread the router cannot record into reports the failure, so the reviewer
// conversation keeps its cursor and retries later.
func TestGuardianThreadEvidenceInjectorReportsUnknownThreadLikeRust(t *testing.T) {
	router, _, _ := newGuardianEvidenceInjectionFixture(t)
	injector, err := NewGuardianThreadEvidenceInjector(router, "missing-thread")
	if err != nil {
		t.Fatalf("NewGuardianThreadEvidenceInjector() error = %v", err)
	}
	raw := json.RawMessage(`{"type":"function_call_output","name":"guardian_original_evidence","namespace":"guardian","output":"{}"}`)
	if err := injector.InjectResponseItems(context.Background(), []json.RawMessage{raw}); err == nil {
		t.Fatalf("injection into an unknown thread succeeded")
	}
	if _, err := NewGuardianThreadEvidenceInjector(nil, "thread"); err == nil {
		t.Fatalf("injector without a router succeeded")
	}
	if _, err := NewGuardianThreadEvidenceInjector(router, "  "); err == nil {
		t.Fatalf("injector without a thread succeeded")
	}
	var missing *GuardianThreadEvidenceInjector
	if err := missing.InjectResponseItems(context.Background(), []json.RawMessage{raw}); err == nil {
		t.Fatalf("nil injector succeeded")
	}
}
