package agentboard

import (
	"context"
	"testing"

	"codex_go/agent"
)

// Rust #58973: one temporary endpoint is shared by the tree's threads and is
// released only after the last participating thread unloads.
func TestMessageBoardMCPDiscoverySharesTreeEndpointLikeRust(t *testing.T) {
	ctx := context.Background()
	host := newTestHost(t, map[string]agent.AgentPath{
		"thread-parent": agent.AgentPathRoot,
		"thread-child":  agent.AgentPath("/root/worker"),
	})
	var discovery MessageBoardMCPDiscovery
	parentEndpoint, parentAuth, err := discovery.AcquireThread(ctx, "tree-1", "thread-parent", host)
	if err != nil {
		t.Fatalf("AcquireThread(parent) error = %v", err)
	}
	childEndpoint, childAuth, err := discovery.AcquireThread(ctx, "tree-1", "thread-child", host)
	if err != nil {
		t.Fatalf("AcquireThread(child) error = %v", err)
	}
	if parentEndpoint != childEndpoint || parentAuth != childAuth {
		t.Fatalf("threads of one tree got different endpoints: %q/%q vs %q/%q", parentEndpoint, parentAuth, childEndpoint, childAuth)
	}
	if discovery.TreeCount() != 1 {
		t.Fatalf("tree endpoints = %d", discovery.TreeCount())
	}
	discovery.ReleaseThread("tree-1", "thread-parent")
	if discovery.TreeCount() != 1 {
		t.Fatalf("releasing one thread dropped the shared endpoint")
	}
	discovery.ReleaseThread("tree-1", "thread-child")
	if discovery.TreeCount() != 0 {
		t.Fatalf("tree endpoint survived the last thread")
	}
}
