package appserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #52535 (`preserve_fork_prefix`) keeps the parent's model-visible history
// on a full fork -- commentary and tool records included -- and marks the copied
// conversation as inherited so a resume cannot recapture the parent's
// authorization. Go's MultiAgent V2 full fork copies the parent record as-is
// (`session.ForkAll`) with no scrub or rebuild path, so it already provides the
// enabled semantics; this test pins that equivalence and the provenance marking.
func TestRuntimeAgentFullForkPreservesParentPrefixLikeRust(t *testing.T) {
	now := time.Now().UTC()
	parentItems := []session.Item{
		{ID: "user-1", Type: "message", Role: "user", Text: "run the tests", CreatedAt: now},
		{ID: "call-1", Type: "function_call", Role: "assistant", CallID: "call-1", Name: "shell", CreatedAt: now},
		{ID: "output-1", Type: "function_call_output", CallID: "call-1", Text: "ok", CreatedAt: now},
		{ID: "assistant-1", Type: "message", Role: "assistant", Text: "tests pass", CreatedAt: now},
		{ID: "commentary-1", Type: "message", Role: "developer", Text: "inherited commentary", CreatedAt: now},
	}
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai"},
		Items:    parentItems}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
	if err := router.threads.RegisterTurn("parent", "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: "parent"}); err != nil {
		t.Fatal(err)
	}
	forkTurns := "all"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", ForkTurns: &forkTurns})
	if err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}
	record, err := store.Read(session.ThreadID(child.AgentID), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if record.ForkedFromID != session.ThreadID("parent") {
		t.Fatalf("ForkedFromID = %q, want the parent thread", record.ForkedFromID)
	}
	byID := map[string]session.Item{}
	for _, item := range record.Items {
		byID[item.ID] = item
	}
	// Tool records are part of the preserved model-visible prefix.
	for _, id := range []string{"user-1", "call-1", "output-1", "assistant-1", "commentary-1"} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("inherited prefix is missing %q: %#v", id, record.Items)
		}
	}
	// Copied conversational messages carry inherited provenance.
	for _, id := range []string{"user-1", "assistant-1"} {
		item := byID[id]
		if !sessionItemInheritedUserMessage(&item) {
			t.Fatalf("%s provenance = %#v, want inherited_user_message", id, item.Data)
		}
	}
	// Go marks every copied item the session model reads as a conversational
	// message: user messages and items tagged assistant, which includes the
	// parent's function call. Rust's enabled mode marks every inherited item, so
	// the tool call carrying provenance is the expected overlap.
	callItem := byID["call-1"]
	if !sessionItemInheritedUserMessage(&callItem) {
		t.Fatalf("call-1 provenance = %#v, want inherited_user_message", callItem.Data)
	}
}

func sessionItemInheritedUserMessage(item *session.Item) bool {
	if item == nil || item.Data == nil {
		return false
	}
	raw, ok := item.Data[harnessMetadataKey]
	if !ok {
		return false
	}
	var metadata map[string]any
	switch value := raw.(type) {
	case json.RawMessage:
		if err := json.Unmarshal(value, &metadata); err != nil {
			return false
		}
	case map[string]any:
		metadata = value
	default:
		return false
	}
	inherited, ok := metadata["inherited_user_message"]
	if !ok {
		return false
	}
	flag, ok := inherited.(bool)
	return ok && flag
}
