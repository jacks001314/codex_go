package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/state"
)

// Rust guardian-v3 host.rs: one worker per root routes durable originals on a
// coalesced wake, EnsureRoot is idempotent, and stopping the root joins the
// worker before the history closes.
func TestGuardianV3HostRoutesOnWakeAndJoinsLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	const root = "guardian-host-root"
	host := NewGuardianV3Host()
	t.Cleanup(host.Close)

	reads := 0
	reader := func(_ context.Context, start agentboard.GuardianHistoryPosition, through *agentboard.GuardianHistoryPosition) (*agentboard.GuardianEvidenceSnapshot, error) {
		reads++
		if reads > 1 {
			return &agentboard.GuardianEvidenceSnapshot{Start: start, End: start}, nil
		}
		end := agentboard.GuardianHistoryPosition{ThreadID: root, EndOrdinalExclusive: 1}
		return &agentboard.GuardianEvidenceSnapshot{
			Start: start,
			End:   end,
			Sources: []agentboard.GuardianCapturedEvidence{{
				ID:       agentboard.GuardianEvidenceID{Ordinal: 1},
				Original: json.RawMessage(`{"type":"message","role":"user","content":"ship it"}`),
				End:      end,
			}},
		}, nil
	}
	decided := make(chan agentboard.GuardianRoutingBatch, 8)
	decider := func(_ context.Context, batch agentboard.GuardianRoutingBatch) (agentboard.GuardianRoutingDecision, error) {
		decided <- batch
		return agentboard.GuardianRoutingDecision{
			Routes:       [][]string{{"release"}},
			Descriptions: []agentboard.GuardianDescriptionUpdate{{Channel: "release", Description: "Shipping"}},
		}, nil
	}
	options := GuardianV3RootOptions{
		Root: root,
		Board: func(ctx context.Context) (agentboard.AgentMessageBoard, error) {
			return agentboard.NewGuardianLocalBoard(sqliteConfig, root).ReadWrite().Open(ctx,
				stubMessageBoardHost{members: map[string]agent.AgentPath{root: agent.AgentPathRoot}})
		},
		Reader:  reader,
		Start:   agentboard.GuardianHistoryPosition{ThreadID: root},
		Decider: decider,
	}
	live, err := host.EnsureRoot(ctx, options)
	if err != nil {
		t.Fatalf("EnsureRoot() error = %v", err)
	}
	again, err := host.EnsureRoot(ctx, options)
	if err != nil {
		t.Fatalf("second EnsureRoot() error = %v", err)
	}
	if again != live {
		t.Fatal("EnsureRoot started a second worker for the same root")
	}
	if !host.Wake(root) {
		t.Fatal("Wake() reported no live root")
	}
	select {
	case batch := <-decided:
		if len(batch.Sources) != 1 || strings.TrimSpace(batch.Sources[0].Text) == "" {
			t.Fatalf("routed batch = %#v", batch)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wake never reached the routing decider")
	}
	deadline := time.Now().Add(5 * time.Second)
	for live.Position().EndOrdinalExclusive < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("routing watermark = %d, want 1", live.Position().EndOrdinalExclusive)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Another wake with no new originals routes nothing.
	host.Wake(root)
	select {
	case <-decided:
		t.Fatal("an empty wake routed another batch")
	case <-time.After(100 * time.Millisecond):
	}
	if err := host.StopRoot(ctx, root); err != nil {
		t.Fatalf("StopRoot() error = %v", err)
	}
	select {
	case <-live.Done():
	default:
		t.Fatal("StopRoot returned before the worker joined")
	}
	if _, ok := host.Root(root); ok {
		t.Fatal("stopped root stayed registered")
	}
	if host.Wake(root) {
		t.Fatal("Wake() succeeded after StopRoot")
	}
}

// A routing failure stops the worker and stays visible on the root.
func TestGuardianV3HostRecordsRoutingFailureLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	const root = "guardian-host-failure"
	host := NewGuardianV3Host()
	t.Cleanup(host.Close)
	reader := func(_ context.Context, start agentboard.GuardianHistoryPosition, _ *agentboard.GuardianHistoryPosition) (*agentboard.GuardianEvidenceSnapshot, error) {
		end := agentboard.GuardianHistoryPosition{ThreadID: root, EndOrdinalExclusive: 1}
		return &agentboard.GuardianEvidenceSnapshot{
			Start: start,
			End:   end,
			Sources: []agentboard.GuardianCapturedEvidence{{
				ID:       agentboard.GuardianEvidenceID{Ordinal: 1},
				Original: json.RawMessage(`{"type":"message","role":"user","content":"hello"}`),
				End:      end,
			}},
		}, nil
	}
	live, err := host.EnsureRoot(ctx, GuardianV3RootOptions{
		Root: root,
		Board: func(ctx context.Context) (agentboard.AgentMessageBoard, error) {
			return agentboard.NewGuardianLocalBoard(sqliteConfig, root).ReadWrite().Open(ctx,
				stubMessageBoardHost{members: map[string]agent.AgentPath{root: agent.AgentPathRoot}})
		},
		Reader: reader,
		Start:  agentboard.GuardianHistoryPosition{ThreadID: root},
		Decider: func(context.Context, agentboard.GuardianRoutingBatch) (agentboard.GuardianRoutingDecision, error) {
			return agentboard.GuardianRoutingDecision{}, errors.New("provider refused the routing batch")
		},
	})
	if err != nil {
		t.Fatalf("EnsureRoot() error = %v", err)
	}
	host.Wake(root)
	deadline := time.Now().Add(5 * time.Second)
	for live.LastError() == nil {
		if time.Now().After(deadline) {
			t.Fatal("routing failure was not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(live.LastError().Error(), "provider refused") {
		t.Fatalf("recorded error = %v", live.LastError())
	}
	if err := host.StopRoot(ctx, root); err != nil {
		t.Fatalf("StopRoot() error = %v", err)
	}
}

// Rust RoutingOutput: the decider parses the model's JSON, tolerates a fenced
// block, and rejects routes or descriptions the housekeeper would repair.
func TestGuardianRoutingDeciderParsesLikeRust(t *testing.T) {
	var instructions, input string
	decider, err := NewGuardianRoutingDecider(func(_ context.Context, prompt string, payload string) (string, error) {
		instructions = prompt
		input = payload
		return "```json\n{\"routes\":[[\"release\"],[\"release\",\"ci\"]],\"descriptions\":[{\"channel\":\"ci\",\"description\":\"builds\"}]}\n```", nil
	}, GuardianV3DeciderOptions{RoutingInstructions: "Group by surface."})
	if err != nil {
		t.Fatalf("NewGuardianRoutingDecider() error = %v", err)
	}
	decision, err := decider(context.Background(), agentboard.GuardianRoutingBatch{
		Catalog: map[string]*string{"release": ptrString("Shipping")},
		Sources: []agentboard.GuardianRoutingSource{
			{ID: agentboard.GuardianEvidenceID{Ordinal: 3}, Text: `{"id":{"ordinal":3},"original":{"type":"message"}}`},
			{ID: agentboard.GuardianEvidenceID{Ordinal: 4}, Text: `{"id":{"ordinal":4},"original":{"type":"message"}}`},
		},
	})
	if err != nil {
		t.Fatalf("decider() error = %v", err)
	}
	if len(decision.Routes) != 2 || len(decision.Routes[1]) != 2 {
		t.Fatalf("routes = %#v", decision.Routes)
	}
	if len(decision.Descriptions) != 1 || decision.Descriptions[0].Channel != "ci" || decision.Descriptions[0].Description != "builds" {
		t.Fatalf("descriptions = %#v", decision.Descriptions)
	}
	if !strings.Contains(instructions, "Group by surface.") {
		t.Fatalf("prompt lost the configured routing instructions:\n%s", instructions)
	}
	var decoded GuardianHousekeeperInput
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		t.Fatalf("input is not JSON: %v", err)
	}
	if len(decoded.Sources) != 2 || len(decoded.Catalog) != 1 {
		t.Fatalf("input catalog/sources = %d/%d", len(decoded.Catalog), len(decoded.Sources))
	}
	if string(decoded.Sources[0]) != `{"id":{"ordinal":3},"original":{"type":"message"}}` {
		t.Fatalf("source 0 = %s", decoded.Sources[0])
	}
}

func TestGuardianRoutingDecisionRejectsInvalidAnswersLikeRust(t *testing.T) {
	cases := map[string]string{
		"empty route":       `{"routes":[[]],"descriptions":[]}`,
		"duplicate channel": `{"routes":[["a"]],"descriptions":[{"channel":"a","description":"x"},{"channel":"a","description":"y"}]}`,
		"unknown field":     `{"routes":[["a"]],"descriptions":[],"extra":1}`,
		"empty channel":     `{"routes":[["a"]],"descriptions":[{"channel":"  ","description":"x"}]}`,
		"not json":          `routes`,
	}
	for name, answer := range cases {
		if _, err := ParseGuardianRoutingDecision(answer); err == nil {
			t.Fatalf("%s: ParseGuardianRoutingDecision accepted %q", name, answer)
		}
	}
	if _, err := ParseGuardianRoutingDecision(`{"routes":[["a"]],"descriptions":[]}`); err != nil {
		t.Fatalf("valid answer rejected: %v", err)
	}
}

func ptrString(value string) *string { return &value }
