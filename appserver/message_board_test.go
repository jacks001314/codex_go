package appserver

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/config"
	"codex_go/session"
)

func TestMessageBoardGateRequiresBothFeaturesLikeRust(t *testing.T) {
	v2 := &config.MultiAgentV2Config{ToolNamespace: "collaboration"}
	both := &config.Config{Values: map[string]any{"features": map[string]any{
		"multi_agent_v2":      true,
		"agent_message_board": true,
	}}}
	if !messageBoardFeatureEnabled(both) || !messageBoardEnabledForTurn(both, false, v2) {
		t.Fatal("both features enabled did not enable the board")
	}
	if messageBoardEnabledForTurn(both, true, v2) {
		t.Fatal("an ephemeral session opened durable board storage")
	}
	inMemory := &config.MultiAgentV2Config{ToolNamespace: "collaboration", MessageBoardInMemory: true}
	if !messageBoardEnabledForTurn(both, true, inMemory) {
		t.Fatal("an ephemeral session did not open the in-memory board")
	}
	boardOnly := &config.Config{Values: map[string]any{"features": map[string]any{"agent_message_board": true}}}
	if messageBoardFeatureEnabled(boardOnly) || messageBoardEnabledForTurn(boardOnly, false, v2) {
		t.Fatal("the board was enabled without multi_agent_v2")
	}
	v2Only := &config.Config{Values: map[string]any{"features": map[string]any{"multi_agent_v2": true}}}
	if messageBoardFeatureEnabled(v2Only) || messageBoardEnabledForTurn(v2Only, false, v2) {
		t.Fatal("the board was enabled without agent_message_board")
	}
	if messageBoardEnabledForTurn(both, false, nil) {
		t.Fatal("the board was enabled without a V2 configuration")
	}
}

// Rust parity: LocalBoardHost resolves the caller's registered tree path, the
// tree's agents, and the caller's clock.
func TestMessageBoardHostResolvesTreeMembershipLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	rootID := "root-thread"
	childID := "child-thread"
	if err := store.Save(&session.Record{
		ID: session.ThreadID(rootID), SessionID: rootID,
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(),
		Metadata: session.Metadata{CWD: home, AgentPath: "/root"},
	}); err != nil {
		t.Fatalf("save root record: %v", err)
	}
	if err := store.Save(&session.Record{
		ID: session.ThreadID(childID), SessionID: rootID, ParentThreadID: session.ThreadID(rootID),
		CreatedAt: time.Unix(2, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
		Metadata: session.Metadata{CWD: home, AgentPath: "/root/worker", AgentDepth: 1},
	}); err != nil {
		t.Fatalf("save child record: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()
	host := &messageBoardHost{router: router, tree: rootID, caller: rootID}

	path, err := host.AgentPath(context.Background(), rootID)
	if err != nil || path != agent.AgentPathRoot {
		t.Fatalf("root AgentPath() = %q, %v", path, err)
	}
	childPath, err := host.AgentPath(context.Background(), childID)
	if err != nil || childPath != agent.AgentPath("/root/worker") {
		t.Fatalf("child AgentPath() = %q, %v", childPath, err)
	}
	resolved, err := host.ResolveAgent(context.Background(), agent.AgentPath("/root/worker"))
	if err != nil || resolved != childID {
		t.Fatalf("ResolveAgent() = %q, %v", resolved, err)
	}
	if _, err := host.ResolveAgent(context.Background(), agent.AgentPath("/root/missing")); err == nil {
		t.Fatal("unknown path resolved")
	}
	if _, err := host.AgentPath(context.Background(), "unknown-thread"); err == nil {
		t.Fatal("unknown thread reported a tree path")
	}
	// The board clock goes through the app-server's current-time request, which
	// needs a subscribed client; that path is covered by the clock-tool tests.
	// This test pins the membership checks the board host makes first.
	if _, err := host.CurrentTime(context.Background(), "unknown-thread"); err == nil {
		t.Fatal("CurrentTime() succeeded for an unknown agent")
	}
}

// An idle recipient is skipped and nothing is queued for a later turn; a
// recipient from another tree is a hard error (Rust's notify contract).
func TestMessageBoardHostNotifyOnlyReachesRunningRecipientsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	rootID := "root-thread"
	childID := "child-thread"
	for _, record := range []*session.Record{
		{ID: session.ThreadID(rootID), SessionID: rootID, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(), Metadata: session.Metadata{CWD: home, AgentPath: "/root"}},
		{ID: session.ThreadID(childID), SessionID: rootID, ParentThreadID: session.ThreadID(rootID), CreatedAt: time.Unix(2, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(), Metadata: session.Metadata{CWD: home, AgentPath: "/root/worker", AgentDepth: 1}},
	} {
		if err := store.Save(record); err != nil {
			t.Fatalf("save record %s: %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()
	host := &messageBoardHost{router: router, tree: rootID, caller: rootID}
	post := agentboard.PostPreview{
		PostMetadata: agentboard.PostMetadata{
			MessageID: "11111111-1111-4111-8111-111111111111", ChannelName: "work",
			Author: agent.AgentPathRoot, ThreadID: "11111111-1111-4111-8111-111111111111",
		},
		TextPreview: "hello",
	}
	delivery, err := host.Notify(context.Background(), childID, post)
	if err != nil {
		t.Fatalf("Notify(idle) error = %v", err)
	}
	if delivery != agentboard.NotificationSkippedInactive {
		t.Fatalf("Notify(idle) = %q, want skipped", delivery)
	}
	if pending := router.requireSteerMailbox().HasPending(childID, "turn-1"); pending {
		t.Fatal("skipped notification queued input for a later turn")
	}

	// A recipient outside this tree is rejected rather than silently skipped.
	otherTree := "other-root"
	if err := store.Save(&session.Record{
		ID: session.ThreadID(otherTree), SessionID: otherTree,
		CreatedAt: time.Unix(3, 0).UTC(), UpdatedAt: time.Unix(3, 0).UTC(),
		Metadata: session.Metadata{CWD: home, AgentPath: "/root"},
	}); err != nil {
		t.Fatalf("save other tree record: %v", err)
	}
	if _, err := host.Notify(context.Background(), otherTree, post); err == nil ||
		!strings.Contains(err.Error(), "another board") {
		t.Fatalf("Notify(other tree) error = %v", err)
	}
}
