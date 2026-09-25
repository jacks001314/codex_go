package appserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

// Rust parity: the local thread store's `thread_data_cleanup` callback deletes
// the boards owned by the permanently removed thread roots, and a subagent's ID
// does not match its parent's board.
type stubMessageBoardHost struct {
	members map[string]agent.AgentPath
}

func (h stubMessageBoardHost) AgentPath(_ context.Context, caller string) (agent.AgentPath, error) {
	path, ok := h.members[caller]
	if !ok {
		return "", fmt.Errorf("unknown agent")
	}
	return path, nil
}

func (h stubMessageBoardHost) ResolveAgent(_ context.Context, path agent.AgentPath) (string, error) {
	for id, member := range h.members {
		if member == path {
			return id, nil
		}
	}
	return "", fmt.Errorf("unknown agent")
}

func (h stubMessageBoardHost) CurrentTime(context.Context, string) (time.Time, error) {
	return time.Now().UTC(), nil
}

func (h stubMessageBoardHost) Notify(context.Context, string, agentboard.PostPreview) (agentboard.NotificationDelivery, error) {
	return agentboard.NotificationAccepted, nil
}

func TestThreadDeleteRemovesOwnedMessageBoardsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	rootID := "root-thread"
	childID := "child-thread"
	for _, record := range []*session.Record{
		{ID: session.ThreadID(rootID), SessionID: rootID, CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(),
			Metadata: session.Metadata{CWD: home, AgentPath: "/root"}},
		{ID: session.ThreadID(childID), SessionID: rootID, ParentThreadID: session.ThreadID(rootID),
			CreatedAt: time.Unix(2, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC(),
			Metadata: session.Metadata{CWD: home, AgentPath: "/root/worker", AgentDepth: 1}},
	} {
		if err := store.Save(record); err != nil {
			t.Fatalf("save record %s: %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	defer router.Close()
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// The board only needs membership and a clock; the production host's clock
	// path goes through the app-server current-time request, which a unit test
	// has no client for.
	host := stubMessageBoardHost{members: map[string]agent.AgentPath{
		rootID: agent.AgentPathRoot, childID: agent.AgentPath("/root/worker"),
	}}
	board, err := agentboard.OpenLocalBoard(ctx, sqliteConfig, rootID, host)
	if err != nil {
		t.Fatalf("OpenLocalBoard() error = %v", err)
	}
	defer board.Close()
	if _, err := board.CreateChannel(ctx, rootID, agentboard.CreateChannelRequest{ChannelName: "work", Subscription: agentboard.Subscribe}); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}

	// Deleting the subagent leaves the tree's board alone.
	if response := router.Handle(requestWithParams(t, IntID(2), MethodThreadDelete, ThreadDeleteParams{ThreadID: childID})); response.Error != nil {
		t.Fatalf("delete child error = %+v", response.Error)
	}
	if _, err := board.CreateChannel(ctx, rootID, agentboard.CreateChannelRequest{ChannelName: "still-here"}); err != nil {
		t.Fatalf("deleting a subagent dropped the tree's board: %v", err)
	}

	// Deleting the root tombstones the board, and the tombstone survives a
	// reopen.
	if response := router.Handle(requestWithParams(t, IntID(3), MethodThreadDelete, ThreadDeleteParams{ThreadID: rootID})); response.Error != nil {
		t.Fatalf("delete root error = %+v", response.Error)
	}
	if _, err := board.CreateChannel(ctx, rootID, agentboard.CreateChannelRequest{ChannelName: "gone"}); err == nil ||
		!strings.Contains(err.Error(), "permanently deleted") {
		t.Fatalf("create after delete error = %v", err)
	}
	board.Close()
	reopened, err := agentboard.OpenLocalBoard(ctx, sqliteConfig, rootID, host)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()
	if _, err := reopened.CreateChannel(ctx, rootID, agentboard.CreateChannelRequest{ChannelName: "gone"}); err == nil ||
		!strings.Contains(err.Error(), "permanently deleted") {
		t.Fatalf("create after reopen error = %v", err)
	}
}

// Rust parity: a thread reuses one board handle across turns, and unloading the
// thread releases it (Rust opens a handle with the thread runtime and drops it
// when the runtime unloads).
func TestThreadUnloadReleasesTheMessageBoardHandleLikeRust(t *testing.T) {
	home := t.TempDir()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(home)),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
	})
	defer router.Close()
	cfg := &config.Config{Values: map[string]any{}}
	v2 := &config.MultiAgentV2Config{ToolNamespace: "collaboration", MessageBoardInMemory: true}

	first, err := router.messageBoardOptionsForTurn(context.Background(), cfg, "thread-1", v2, nil)
	if err != nil || first == nil {
		t.Fatalf("messageBoardOptionsForTurn() = %#v, %v", first, err)
	}
	if router.messageBoards.Len() != 1 {
		t.Fatalf("live boards = %d, want one for the loaded thread", router.messageBoards.Len())
	}
	again, err := router.messageBoardOptionsForTurn(context.Background(), cfg, "thread-1", v2, nil)
	if err != nil || again == nil || again.Board != first.Board {
		t.Fatalf("the thread opened a second handle: %#v", again)
	}
	if router.messageBoards.Len() != 1 {
		t.Fatalf("live boards = %d, want the handle reused", router.messageBoards.Len())
	}

	router.markThreadUnloaded("thread-1")
	if router.messageBoards.Len() != 0 {
		t.Fatalf("live boards = %d, want the handle released on unload", router.messageBoards.Len())
	}
	// A thread loaded again after the unload opens a fresh handle.
	reopened, err := router.messageBoardOptionsForTurn(context.Background(), cfg, "thread-1", v2, nil)
	if err != nil || reopened == nil || reopened.Board == first.Board {
		t.Fatalf("handle after unload = %#v, %v", reopened, err)
	}
}

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
