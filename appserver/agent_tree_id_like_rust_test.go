package appserver

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"codex_go/agent"
	"codex_go/session"
)

// TestAgentTreeIDChangesOnReloadAndIsSharedByChildrenLikeRust pins Rust #60190:
// the shutdown-report correlation id is a tree id generated when the tree is
// loaded, shared by every controller in that tree including children, and never
// the root thread id - so two loads of the same tree report different ids.
func TestAgentTreeIDChangesOnReloadAndIsSharedByChildrenLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	for _, record := range []*session.Record{
		{ID: "tree-root", SessionID: "tree-root", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{CWD: t.TempDir(), Model: "model", ModelProvider: "openai", AgentPath: "/root"}},
		{ID: "tree-child", SessionID: "tree-child", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			ParentThreadID: "tree-root",
			Metadata:       session.Metadata{CWD: t.TempDir(), Model: "model", ModelProvider: "openai", AgentPath: "/root/worker", AgentDepth: 1}},
	} {
		if err := store.Create(record); err != nil {
			t.Fatalf("create %s: %v", record.ID, err)
		}
	}
	newController := func(router *RuntimeRouter, threadID string) *runtimeAgentController {
		t.Helper()
		return newRuntimeAgentControllerForTurn(router, threadID, "turn-1", "root-turn", "", "", t.TempDir(), 4, agent.VersionV2, nil).(*runtimeAgentController)
	}

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	root := newController(router, "tree-root")
	if root.treeID == "" {
		t.Fatal("tree id is empty")
	}
	if _, err := uuid.Parse(root.treeID); err != nil {
		t.Fatalf("tree id %q is not a UUID: %v", root.treeID, err)
	}
	if root.treeID == root.rootID {
		t.Fatalf("tree id %q reuses the root thread id", root.treeID)
	}

	// A second controller for the same tree - another turn of the root thread or
	// any of its children - shares the id.
	sameTree := newController(router, "tree-root")
	child := newController(router, "tree-child")
	if sameTree.treeID != root.treeID {
		t.Fatalf("second controller of the same tree has id %q, want %q", sameTree.treeID, root.treeID)
	}
	if child.treeID != root.treeID {
		t.Fatalf("child controller has id %q, want the shared tree id %q", child.treeID, root.treeID)
	}

	// A reload is a new router: the same tree gets a different correlation id.
	reloaded := newController(NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)}), "tree-root")
	if reloaded.treeID == root.treeID {
		t.Fatalf("tree id %q survived a reload, want a new id per load", reloaded.treeID)
	}

	// The shutdown report carries the tree id, not the root thread id.
	if reportID := root.agentTreeID(); reportID != root.treeID {
		t.Fatalf("shutdown report tree id = %q, want %q", reportID, root.treeID)
	}
}
