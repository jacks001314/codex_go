package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// Rust #51329: partial-history subagent forks are gone. `all` and the legacy
// positive-integer `fork_turns` spellings both inherit the parent's full
// history, `none` starts the child without parent history, and zero or
// non-numeric values are rejected with the `none`/`all` message. Rust's
// regression coverage is `multi_agent_v2_spawn_rejects_invalid_fork_turns_string`
// (parameterized over "banana" and "0") plus the legacy "1"/"3" full-history
// cases.
func TestRuntimeAgentControllerForkTurnsLikeRust(t *testing.T) {
	parentItems := func(now time.Time) []session.Item {
		return []session.Item{
			{ID: "item-1", Type: "message", Role: "user", Text: "one", CreatedAt: now},
			{ID: "item-2", Type: "message", Role: "assistant", Text: "two", CreatedAt: now},
			{ID: "item-3", Type: "message", Role: "user", Text: "three", CreatedAt: now},
		}
	}
	for _, testCase := range []struct {
		name      string
		forkTurns *string
		wantItems int
		wantErr   string
	}{
		{name: "all", forkTurns: forkTurnsStringPtr("all"), wantItems: 3},
		{name: "omitted defaults to all", wantItems: 3},
		{name: "legacy one turn inherits the full history", forkTurns: forkTurnsStringPtr("1"), wantItems: 3},
		{name: "legacy three turns inherit the full history", forkTurns: forkTurnsStringPtr("3"), wantItems: 3},
		{name: "none starts without parent history", forkTurns: forkTurnsStringPtr("none"), wantItems: 0},
		{name: "zero is rejected", forkTurns: forkTurnsStringPtr("0"), wantErr: "fork_turns must be `none` or `all`"},
		{name: "banana is rejected", forkTurns: forkTurnsStringPtr("banana"), wantErr: "fork_turns must be `none` or `all`"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			store := session.NewStore(t.TempDir())
			now := time.Now().UTC()
			parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
				Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai"},
				Items:    parentItems(now)}
			if err := store.Create(parent); err != nil {
				t.Fatal(err)
			}
			router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store), Config: config.NewConfigService(home)})
			controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
			if err := router.threads.RegisterTurn("parent", "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: "parent"}); err != nil {
				t.Fatal(err)
			}
			child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", ForkTurns: testCase.forkTurns})
			if testCase.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("SpawnAgent() error = %v, want %q", err, testCase.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SpawnAgent() error = %v", err)
			}
			record, err := store.Read(session.ThreadID(child.AgentID), false, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(record.Items) != testCase.wantItems {
				t.Fatalf("inherited items = %d (%#v), want %d", len(record.Items), record.Items, testCase.wantItems)
			}
			if testCase.wantItems > 0 && record.ForkedFromID != session.ThreadID("parent") {
				t.Fatalf("ForkedFromID = %q, want the parent thread", record.ForkedFromID)
			}
		})
	}
}

func forkTurnsStringPtr(value string) *string {
	return &value
}
