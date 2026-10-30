package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// TestCompactionInstallsFullReplacementContextLikeRust covers Rust #51117
// (`8571b9eaa4`, "Install full context in compaction replacement history"):
// manual/pre-turn/post-turn compaction installs the refreshed initial context
// before the compaction summary, and persists the replacement window's full
// world-state and turn-context baselines. The summary is produced by the
// previous model while the replacement context belongs to the current model.
func TestCompactionInstallsFullReplacementContextLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	cwd := t.TempDir()
	const threadID = "thread-compaction-full-context"
	created := time.Now().UTC()
	oldInstructions := "Old base instructions."
	refreshedInstructions := "Refreshed base instructions."
	definitions := incrementalCatalogLikeRust("Run a command.", incrementalToolSearchLikeRust())
	items := append(declarationPrefixSessionItemsLikeRust(t, definitions, oldInstructions, threadID, created),
		session.Item{ID: "u1", Type: "message", Role: "user", Text: "first request", CreatedAt: created},
		session.Item{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer", CreatedAt: created},
	)
	if err := store.Save(&session.Record{
		ID: threadID, SessionID: threadID, CreatedAt: created, UpdatedAt: created, RecencyAt: created,
		Metadata: session.Metadata{
			CWD: cwd, Model: "gpt-previous", BaseInstructions: refreshedInstructions,
			Extra: map[string]any{},
		},
		Items: items,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:  NewRouter(store),
		CompactRunner: &recordingCompactRunner{summary: "full replacement summary"},
		DefaultCWD:    cwd,
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-previous", ContextWindow: 200000, CompHash: "hash-previous"},
			{Slug: "gpt-current", ContextWindow: 200000, CompHash: "hash-current"},
		}})),
	})
	router.SetNotificationSink(NewNotificationBuffer())

	if _, err := router.compactThread(context.Background(), &runtimeCompactRequest{
		ThreadID: threadID, TurnID: "turn-compact-current",
		Trigger: compact.TriggerManual, Reason: compact.ReasonUserRequested, Phase: compact.PhaseStandaloneTurn,
		Model: "gpt-previous", ReplacementModel: "gpt-current", WindowModel: "gpt-current",
		ReplacementParams: &turn.TurnStartParams{ThreadID: threadID, Model: "gpt-current", CWD: cwd},
		RemoteOnly:        true,
	}); err != nil {
		t.Fatalf("compactThread() error = %v", err)
	}

	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	refreshedIndex, environmentIndex, summaryIndex := -1, -1, -1
	for i := range record.Items {
		text := record.Items[i].Text
		switch {
		case text == oldInstructions:
			t.Fatalf("replacement history kept the replaced instructions: %#v", record.Items)
		case text == refreshedInstructions:
			refreshedIndex = i
		case strings.Contains(text, "<environment_context>"):
			environmentIndex = i
		case strings.HasPrefix(text, compact.SummaryPrefix):
			summaryIndex = i
		}
	}
	if refreshedIndex < 0 {
		t.Fatalf("replacement history is missing the refreshed instructions: %#v", record.Items)
	}
	if environmentIndex < 0 {
		t.Fatalf("replacement history dropped the environment context: %#v", record.Items)
	}
	if summaryIndex < 0 {
		t.Fatalf("replacement history is missing the compaction summary: %#v", record.Items)
	}
	if refreshedIndex > summaryIndex || environmentIndex > summaryIndex {
		t.Fatalf("replacement context is not ahead of the compaction summary: %#v", record.Items)
	}
	if summaryIndex != len(record.Items)-2 {
		t.Fatalf("compaction summary index = %d of %d items, want it immediately before the compaction marker", summaryIndex, len(record.Items))
	}

	path, err := rollout.FindThreadPath(store.Root(), threadID, false)
	if err != nil {
		t.Fatalf("rollout path error = %v", err)
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	compactedIndex := -1
	for i := range lines {
		if lines[i].Type == "compacted" {
			compactedIndex = i
		}
	}
	if compactedIndex < 0 || compactedIndex+2 >= len(lines) {
		t.Fatalf("rollout does not carry the compacted baseline pair: %#v", lines)
	}
	if lines[compactedIndex+1].Type != "world_state" || lines[compactedIndex+2].Type != "turn_context" {
		t.Fatalf("rollout after compacted = %q, %q; want world_state then turn_context",
			lines[compactedIndex+1].Type, lines[compactedIndex+2].Type)
	}
	var worldState struct {
		Full  bool               `json:"full"`
		State session.WorldState `json:"state"`
	}
	if err := json.Unmarshal(lines[compactedIndex+1].WorldState, &worldState); err != nil {
		t.Fatalf("decode world_state: %v", err)
	}
	if !worldState.Full {
		t.Fatalf("world_state baseline is not marked full: %s", lines[compactedIndex+1].WorldState)
	}
	if got, ok := decodeWorldStateModel(worldState.State.Model); !ok || got != "gpt-current" {
		t.Fatalf("world_state model = %q,%v, want the replacement model gpt-current", got, ok)
	}
	if len(worldState.State.TopLevelTools) == 0 || len(worldState.State.BaseInstructions) == 0 {
		t.Fatalf("world_state baseline is incomplete: %s", lines[compactedIndex+1].WorldState)
	}
	var turnContext rollout.TurnContextRecord
	if err := json.Unmarshal(lines[compactedIndex+2].TurnContext, &turnContext); err != nil {
		t.Fatalf("decode turn_context: %v", err)
	}
	if turnContext.Model != "gpt-current" {
		t.Fatalf("replacement turn_context model = %q, want gpt-current", turnContext.Model)
	}
}
