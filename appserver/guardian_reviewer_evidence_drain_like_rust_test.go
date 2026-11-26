package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/state"
)

// Rust #62136: a reviewer's queued originals are drained from its inbox by the
// verdict that follows, once per conversation. This exercises the live host
// wiring: routing through the housekeeper queues the post, and the reviewer
// drain returns it exactly once.
func TestGuardianV3ReviewerEvidenceDrainsLiveInboxLikeRust(t *testing.T) {
	ctx := context.Background()
	sqliteConfig, err := state.NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const root = "guardian-evidence-root"
	host := NewGuardianV3Host()
	t.Cleanup(host.Close)
	reads := 0
	reader := func(_ context.Context, start agentboard.GuardianHistoryPosition, _ *agentboard.GuardianHistoryPosition) (*agentboard.GuardianEvidenceSnapshot, error) {
		reads++
		if reads > 1 {
			return &agentboard.GuardianEvidenceSnapshot{Start: start, End: start}, nil
		}
		end := agentboard.GuardianHistoryPosition{ThreadID: root, EndOrdinalExclusive: 1}
		return &agentboard.GuardianEvidenceSnapshot{
			Start: start,
			End:   end,
			Sources: []agentboard.GuardianCapturedEvidence{{
				ID:       agentboard.GuardianEvidenceID{Ordinal: 0},
				Original: json.RawMessage(`{"type":"message","role":"user","content":"routed payload"}`),
				End:      end,
			}},
		}, nil
	}
	decider := func(_ context.Context, batch agentboard.GuardianRoutingBatch) (agentboard.GuardianRoutingDecision, error) {
		routes := make([][]string, len(batch.Sources))
		for index := range routes {
			routes[index] = []string{"release"}
		}
		return agentboard.GuardianRoutingDecision{
			Routes:       routes,
			Descriptions: []agentboard.GuardianDescriptionUpdate{{Channel: "release", Description: "Shipping"}},
		}, nil
	}
	live, err := host.EnsureRoot(ctx, GuardianV3RootOptions{
		Root: root,
		Board: func(ctx context.Context) (agentboard.AgentMessageBoard, error) {
			return agentboard.NewGuardianLocalBoard(sqliteConfig, root).ReadWrite().Open(ctx,
				stubMessageBoardHost{members: map[string]agent.AgentPath{root: agent.AgentPathRoot}})
		},
		Reader:  reader,
		Start:   agentboard.GuardianHistoryPosition{ThreadID: root},
		Decider: decider,
	})
	if err != nil {
		t.Fatalf("EnsureRoot() error = %v", err)
	}
	// The reviewer subscribes before the original is committed, because the
	// housekeeper queues for the subscribers present at insertion.
	if err := live.Delivery().Inbox(root).SetSubscription("release", true); err != nil {
		t.Fatalf("SetSubscription() error = %v", err)
	}
	if !host.Wake(root) {
		t.Fatal("Wake() reported no live root")
	}
	deadline := time.Now().Add(5 * time.Second)
	for live.Delivery().Inbox(root).Len() < 1 {
		if err := live.LastError(); err != nil {
			t.Fatalf("routing failed: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("reviewer inbox never received the routed original (watermark %d)", live.Position().EndOrdinalExclusive)
		}
		time.Sleep(10 * time.Millisecond)
	}
	router := NewRuntimeRouter(RuntimeServices{})
	router.guardianV3Host = host
	items, through, err := router.guardianV3ReviewerEvidence(ctx, root, 0)
	if err != nil {
		t.Fatalf("guardianV3ReviewerEvidence() error = %v", err)
	}
	if len(items) != 1 || through != 1 {
		t.Fatalf("drained items = %#v through = %d, want one original", items, through)
	}
	if !strings.Contains(items[0].Text, "routed payload") || items[0].Namespace != "guardian" {
		t.Fatalf("drained item = %#v", items[0])
	}
	again, throughAgain, err := router.guardianV3ReviewerEvidence(ctx, root, through)
	if err != nil {
		t.Fatalf("second guardianV3ReviewerEvidence() error = %v", err)
	}
	if len(again) != 0 || throughAgain != through {
		t.Fatalf("second drain returned %#v through = %d, want nothing new", again, throughAgain)
	}
}
