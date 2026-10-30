package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
)

func newAgentSendRequestTestRouter(t *testing.T) (*RuntimeRouter, string) {
	t.Helper()
	home := t.TempDir()
	cwd := t.TempDir()
	now := time.Now().UTC()
	store := session.NewStore(t.TempDir())
	records := []*session.Record{
		{
			ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: cwd, AgentPath: "/root"},
		},
		{
			ID: "worker", SessionID: "root", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: cwd, AgentPath: "/root/worker", AgentDepth: 1, AgentNickname: "Worker"},
		},
		{
			ID: "worker-peer", SessionID: "root", ParentThreadID: "worker", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: cwd, AgentPath: "/root/worker/peer", AgentDepth: 2},
		},
		{
			ID: "root-peer", SessionID: "root", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: cwd, AgentPath: "/root/peer", AgentDepth: 1},
		},
	}
	for _, record := range records {
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", record.ID, err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	return router, cwd
}

// Rust #46994 resolves a target reference relative to the caller's registered
// identity and returns the recipient metadata from the accepted send request.
// Go carries that metadata back through SendMessageArgs for the activity event.
func TestRuntimeAgentMessageUsesCapturedCallerIdentityLikeRust(t *testing.T) {
	router, cwd := newAgentSendRequestTestRouter(t)
	controller, ok := newRuntimeAgentControllerWithVersion(router, "worker", cwd, 8, agent.VersionV2).(agent.V2ToolController)
	if !ok {
		t.Fatal("runtime controller does not implement V2ToolController")
	}
	router.markThreadUnloaded("worker-peer")

	args := &agent.SendMessageArgs{Target: "peer", Message: "captured delivery", Plaintext: true}
	if err := controller.SendMessage(context.Background(), args); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if args.ResolvedThreadID != "worker-peer" || args.ResolvedPath != "/root/worker/peer" {
		t.Fatalf("resolved receipt = %s at %s, want worker-peer at /root/worker/peer", args.ResolvedThreadID, args.ResolvedPath)
	}
	if active := router.threads.ActiveTurn("worker-peer"); active != nil {
		t.Fatalf("queue-only delivery woke the recipient: %#v", active)
	}
	queued := router.drainRuntimeAgentMessages("worker-peer")
	if len(queued) != 1 {
		t.Fatalf("queued messages = %#v, want one delivery", queued)
	}
	assertAgentMessageRoute(t, queued[0], "/root/worker", "/root/worker/peer")
	if decoy := router.drainRuntimeAgentMessages("root-peer"); len(decoy) != 0 {
		t.Fatalf("reference resolved outside the caller subtree: %#v", decoy)
	}
}

// Rust #46994 preserves the non-waking QueueOnly mode and still rejects a
// follow-up task aimed at the root agent.
func TestRuntimeAgentFollowupToRootIsRejectedLikeRust(t *testing.T) {
	router, cwd := newAgentSendRequestTestRouter(t)
	controller, ok := newRuntimeAgentControllerWithVersion(router, "worker", cwd, 8, agent.VersionV2).(agent.V2ToolController)
	if !ok {
		t.Fatal("runtime controller does not implement V2ToolController")
	}

	args := &agent.FollowupTaskArgs{Target: "/root", Message: "wake root", Plaintext: true}
	err := controller.FollowupTask(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "follow-up tasks can't target the root agent") {
		t.Fatalf("FollowupTask() error = %v", err)
	}
	if args.ResolvedThreadID != "" || args.ResolvedPath != "" {
		t.Fatalf("rejected follow-up published a receipt: %s at %s", args.ResolvedThreadID, args.ResolvedPath)
	}
	if active := router.threads.ActiveTurn("root"); active != nil {
		t.Fatalf("rejected follow-up started a root turn: %#v", active)
	}
	if queued := router.drainRuntimeAgentMessages("root"); len(queued) != 0 {
		t.Fatalf("rejected follow-up queued mail: %#v", queued)
	}
}
