package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #61344 `retained_items_for_fresh_child`: a child spawned without forked
// history still inherits the parent's explicitly retained items, recorded as
// inherited context so a resume cannot treat them as the child's own evidence.
func TestRuntimeAgentFreshSpawnInheritsRetainedItemsLikeRust(t *testing.T) {
	now := time.Now().UTC()
	parentItems := []session.Item{
		{ID: "user-1", Type: "message", Role: "user", Text: "run the tests", CreatedAt: now},
		{ID: "ordinary", Type: "message", Role: "assistant", Text: "tests pass", CreatedAt: now},
		{
			ID: "retained", Type: "function_call_output", Name: "shell", Text: "always check the release branch", CreatedAt: now,
			Data: map[string]any{"harness_metadata": map[string]any{
				"retain_across_windows":         true,
				"retained_source":               map[string]any{"revision": "retained_1", "complete": true},
				"fallback_token_limit_override": 24000,
				"image_replay_sources":          []any{map[string]any{"detail": "auto"}},
				"user_input_order":              7,
			}},
		},
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
	forkTurns := "none"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", ForkTurns: &forkTurns})
	if err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}
	record, err := store.Read(session.ThreadID(child.AgentID), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Items) != 1 {
		t.Fatalf("fresh child items = %#v, want only the retained item", record.Items)
	}
	inherited := record.Items[0]
	if inherited.ID != "retained" {
		t.Fatalf("inherited item = %#v", inherited)
	}
	var metadata map[string]any
	if err := json.Unmarshal(harnessMetadataRawFromItem(&inherited), &metadata); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if metadata["inherited_user_message"] != true {
		t.Fatalf("inherited metadata = %#v", metadata)
	}
	if metadata["retain_across_windows"] != true {
		t.Fatalf("retention decision lost: %#v", metadata)
	}
	if metadata["retained_source"] == nil || metadata["fallback_token_limit_override"] != float64(24000) || metadata["image_replay_sources"] == nil {
		t.Fatalf("replay evidence lost: %#v", metadata)
	}
	if _, order := metadata["user_input_order"]; order {
		t.Fatalf("parent acceptance order survived: %#v", metadata)
	}
}

// A full-history fork keeps the retained item as part of the copied prefix and
// marks it inherited, like any other explicitly retained item.
func TestRuntimeAgentFullForkMarksRetainedItemsInheritedLikeRust(t *testing.T) {
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent-fork", SessionID: "parent-fork", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai"},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "run the tests", CreatedAt: now},
			{ID: "retained", Type: "function_call_output", Name: "shell", Text: "keep", CreatedAt: now,
				Data: map[string]any{"harness_metadata": map[string]any{"retain_across_windows": true}}},
		}}
	store := session.NewStore(t.TempDir())
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(t.TempDir())})
	controller := newRuntimeAgentControllerForTurn(router, "parent-fork", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
	if err := router.threads.RegisterTurn("parent-fork", "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: "parent-fork"}); err != nil {
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
	var retained *session.Item
	for index := range record.Items {
		if record.Items[index].ID == "retained" {
			retained = &record.Items[index]
			break
		}
	}
	if retained == nil {
		t.Fatalf("fork lost the retained item: %#v", record.Items)
	}
	if !sessionItemIsAlwaysRetained(retained) {
		t.Fatalf("retained decision lost: %#v", retained.Data)
	}
	metadata := harnessMetadataFromSessionItem(retained)
	if metadata == nil || !metadata.InheritedUserMessage {
		t.Fatalf("retained item provenance = %#v, want inherited", metadata)
	}
}

// A retained message that exceeds the shared retention budget reaches the child
// truncated, keeps its retention policy, and no longer replays its full raw
// payload.
func TestRuntimeAgentFreshSpawnTruncatesRetainedItemsLikeRust(t *testing.T) {
	now := time.Now().UTC()
	originalText := strings.Repeat("a", 300_000)
	parent := &session.Record{ID: "parent-long", SessionID: "parent-long", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai"},
		Items: []session.Item{{
			ID: "retained-long", Type: "message", Role: "developer", Text: originalText, CreatedAt: now,
			Raw:  json.RawMessage(`{"type":"message","role":"developer","content":[{"type":"input_text","text":"` + originalText + `"}]}`),
			Data: map[string]any{"harness_metadata": map[string]any{"retain_across_windows": true}},
		}}}
	store := session.NewStore(t.TempDir())
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(t.TempDir())})
	controller := newRuntimeAgentControllerForTurn(router, "parent-long", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
	if err := router.threads.RegisterTurn("parent-long", "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: "parent-long"}); err != nil {
		t.Fatal(err)
	}
	forkTurns := "none"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", ForkTurns: &forkTurns})
	if err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}
	record, err := store.Read(session.ThreadID(child.AgentID), false, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Items) != 1 {
		t.Fatalf("fresh child items = %d, want the retained message", len(record.Items))
	}
	inherited := record.Items[0]
	if len(inherited.Text) >= len(originalText) {
		t.Fatalf("inherited text was not truncated: %d >= %d", len(inherited.Text), len(originalText))
	}
	if len(inherited.Raw) != 0 {
		t.Fatalf("inherited item kept the untruncated raw payload: %d bytes", len(inherited.Raw))
	}
	metadata := harnessMetadataFromSessionItem(&inherited)
	if metadata == nil || !metadata.InheritedUserMessage {
		t.Fatalf("inherited provenance = %#v", metadata)
	}
	if metadata.RetainAcrossWindows == nil || !*metadata.RetainAcrossWindows {
		t.Fatalf("inherited retention = %#v", metadata.RetainAcrossWindows)
	}
}

// retentionScopedItem builds a developer message with one #62654 policy.
func retentionScopedItem(id string, text string, now time.Time, retainAcrossWindows bool, retainInChildren *bool) session.Item {
	metadata := map[string]any{"retain_across_windows": retainAcrossWindows}
	if retainInChildren != nil {
		metadata["retain_in_children"] = *retainInChildren
	}
	return session.Item{
		ID: id, Type: "message", Role: "developer", Text: text, CreatedAt: now,
		Data: map[string]any{"harness_metadata": metadata},
	}
}

// spawnChildWithRetentionPolicies spawns one worker from a parent holding every
// retention policy and returns the child's persisted items.
func spawnChildWithRetentionPolicies(t *testing.T, parentID string, forkTurns string) []session.Item {
	t.Helper()
	now := time.Now().UTC()
	threadOnly := false
	childrenOnly := true
	parent := &session.Record{ID: session.ThreadID(parentID), SessionID: parentID, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai"},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "run the tests", CreatedAt: now},
			retentionScopedItem("thread-only", "Send progress updates.", now, true, &threadOnly),
			retentionScopedItem("children-only", "Read the worker log.", now, false, &childrenOnly),
			retentionScopedItem("shared", "Keep the release branch green.", now, true, nil),
		}}
	store := session.NewStore(t.TempDir())
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(t.TempDir())})
	controller := newRuntimeAgentControllerForTurn(router, parentID, "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
	if err := router.threads.RegisterTurn(parentID, "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: parentID}); err != nil {
		t.Fatal(err)
	}
	fork := forkTurns
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", ForkTurns: &fork})
	if err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}
	record, err := store.Read(session.ThreadID(child.AgentID), false, true)
	if err != nil {
		t.Fatal(err)
	}
	return record.Items
}

func retainedItemTexts(items []session.Item) []string {
	texts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type == "message" && item.Role == "developer" {
			texts = append(texts, item.Text)
		}
	}
	return texts
}

// Rust #62654 inherits by child scope, not window scope: a fresh child receives
// the `alwaysInChildren` and legacy `always` instructions and never the one the
// client kept for the parent thread only.
func TestRuntimeAgentFreshChildFollowsChildRetentionLikeRust(t *testing.T) {
	got := retainedItemTexts(spawnChildWithRetentionPolicies(t, "parent-child-scope", "none"))
	want := []string{"Read the worker log.", "Keep the release branch green."}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("fresh child instructions = %v, want %v", got, want)
	}
}

// A full-history fork keeps its ordinary inheritance rules but must drop an item
// the client excluded from children, so `alwaysInThread` never outlives the parent
// thread even when every turn is forked.
func TestRuntimeAgentFullForkDropsThreadOnlyRetentionLikeRust(t *testing.T) {
	items := spawnChildWithRetentionPolicies(t, "parent-fork-scope", "all")
	for _, item := range items {
		if item.ID == "thread-only" || item.Text == "Send progress updates." {
			t.Fatalf("fork inherited a thread-only instruction: %#v", item)
		}
	}
	got := retainedItemTexts(items)
	for _, want := range []string{"Read the worker log."} {
		found := false
		for _, text := range got {
			if text == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fork instructions = %v, want %q", got, want)
		}
	}
}
