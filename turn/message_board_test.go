package turn

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/agentboard"
	"codex_go/tool"
)

// stubBoardHost is the minimal agentboard.Host a turn test needs: a fixed
// membership map, a wall clock and an accepting notification sink.
type stubBoardHost struct {
	mu      sync.Mutex
	members map[string]agent.AgentPath
}

func (h *stubBoardHost) AgentPath(_ context.Context, caller string) (agent.AgentPath, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	path, ok := h.members[caller]
	if !ok {
		return "", fmt.Errorf("unknown agent")
	}
	return path, nil
}

func (h *stubBoardHost) ResolveAgent(_ context.Context, path agent.AgentPath) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, member := range h.members {
		if member == path {
			return id, nil
		}
	}
	return "", fmt.Errorf("unknown agent")
}

func (h *stubBoardHost) CurrentTime(_ context.Context, _ string) (time.Time, error) {
	return time.Now().UTC(), nil
}

func (h *stubBoardHost) Notify(context.Context, string, agentboard.PostPreview) (agentboard.NotificationDelivery, error) {
	return agentboard.NotificationAccepted, nil
}

// Rust parity: the message-board contribution publishes the same nine tools
// inside the host-configured namespace, with the namespace description the host
// supplies.
func TestMessageBoardOptionsRegisterTheNineToolsLikeRust(t *testing.T) {
	root := "root-thread"
	host := &stubBoardHost{members: map[string]agent.AgentPath{root: agent.AgentPathRoot}}
	board := (&agentboard.InMemoryMessageBoards{}).Open(root, host)
	registry := tool.NewRegistry()
	override := "Catalog post."
	options := &MessageBoardOptions{
		Board:                board,
		Caller:               root,
		CallerPath:           agent.AgentPathRoot,
		Namespace:            "delegation",
		NamespaceDescription: agent.MultiAgentV2NamespaceDescription,
		ToolOverrides:        map[string]agentboard.MessageBoardToolOverride{"post": {Description: &override}},
	}
	if err := registerMessageBoardTools(registry, options); err != nil {
		t.Fatalf("registerMessageBoardTools() error = %v", err)
	}
	specs := registry.ModelVisibleSpecs()
	if len(specs) != len(agentboard.MessageBoardToolNames) {
		t.Fatalf("registered %d tools, want %d", len(specs), len(agentboard.MessageBoardToolNames))
	}
	for _, spec := range specs {
		if spec.Name.Namespace != "delegation" || spec.NamespaceDescription != agent.MultiAgentV2NamespaceDescription {
			t.Fatalf("%s namespace = %q/%q", spec.Name.Name, spec.Name.Namespace, spec.NamespaceDescription)
		}
		if spec.Name.Name == "post" && spec.Description != override {
			t.Fatalf("post description = %q, want the catalog override", spec.Description)
		}
	}

	// A nil board contributes nothing, so a host without a board keeps the
	// delegation surface unchanged.
	empty := tool.NewRegistry()
	if err := registerMessageBoardTools(empty, &MessageBoardOptions{}); err != nil {
		t.Fatalf("registerMessageBoardTools(empty) error = %v", err)
	}
	if specs := empty.ModelVisibleSpecs(); len(specs) != 0 {
		t.Fatalf("empty options registered %d tools", len(specs))
	}
}
