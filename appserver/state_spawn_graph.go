package appserver

import (
	"context"

	"codex_go/agent"
	"codex_go/state"
)

type stateSpawnGraph struct {
	runtime *state.StateRuntime
}

func (s *stateSpawnGraph) UpsertThreadSpawnEdge(parentThreadID string, childThreadID string, status agent.ThreadSpawnEdgeStatus) error {
	return s.runtime.UpsertThreadSpawnEdge(context.Background(), parentThreadID, childThreadID, string(status))
}

func (s *stateSpawnGraph) SetThreadSpawnEdgeStatus(childThreadID string, status agent.ThreadSpawnEdgeStatus) error {
	return s.runtime.SetThreadSpawnEdgeStatus(context.Background(), childThreadID, string(status))
}

func (s *stateSpawnGraph) ListThreadSpawnChildren(parentThreadID string, statusFilter *agent.ThreadSpawnEdgeStatus) ([]string, error) {
	return s.runtime.ListThreadSpawnChildren(context.Background(), parentThreadID, spawnEdgeStatusString(statusFilter))
}

func (s *stateSpawnGraph) ListThreadSpawnDescendants(rootThreadID string, statusFilter *agent.ThreadSpawnEdgeStatus) ([]string, error) {
	return s.runtime.ListThreadSpawnDescendants(context.Background(), rootThreadID, spawnEdgeStatusString(statusFilter))
}

func spawnEdgeStatusString(status *agent.ThreadSpawnEdgeStatus) *string {
	if status == nil {
		return nil
	}
	value := string(*status)
	return &value
}

var _ agent.Store = (*stateSpawnGraph)(nil)
