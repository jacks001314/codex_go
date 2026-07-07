package agent

import (
	"sort"
	"sync"
)

type ThreadSpawnEdge struct {
	ParentThreadID string                `json:"parentThreadId"`
	ChildThreadID  string                `json:"childThreadId"`
	Status         ThreadSpawnEdgeStatus `json:"status"`
}

type Store interface {
	UpsertThreadSpawnEdge(parentThreadID string, childThreadID string, status ThreadSpawnEdgeStatus) error
	SetThreadSpawnEdgeStatus(childThreadID string, status ThreadSpawnEdgeStatus) error
	ListThreadSpawnChildren(parentThreadID string, statusFilter *ThreadSpawnEdgeStatus) ([]string, error)
	ListThreadSpawnDescendants(rootThreadID string, statusFilter *ThreadSpawnEdgeStatus) ([]string, error)
}

type MemoryStore struct {
	mu    sync.Mutex
	edges map[string]ThreadSpawnEdge
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{edges: map[string]ThreadSpawnEdge{}}
}

func (s *MemoryStore) UpsertThreadSpawnEdge(parentThreadID string, childThreadID string, status ThreadSpawnEdgeStatus) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.edges == nil {
		s.edges = map[string]ThreadSpawnEdge{}
	}
	s.edges[childThreadID] = ThreadSpawnEdge{ParentThreadID: parentThreadID, ChildThreadID: childThreadID, Status: status}
	return nil
}

func (s *MemoryStore) SetThreadSpawnEdgeStatus(childThreadID string, status ThreadSpawnEdgeStatus) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	edge, ok := s.edges[childThreadID]
	if !ok {
		return nil
	}
	edge.Status = status
	s.edges[childThreadID] = edge
	return nil
}

func (s *MemoryStore) ListThreadSpawnChildren(parentThreadID string, statusFilter *ThreadSpawnEdgeStatus) ([]string, error) {
	if s == nil {
		return []string{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	children := make([]string, 0)
	for _, edge := range s.edges {
		if edge.ParentThreadID != parentThreadID {
			continue
		}
		if statusFilter != nil && edge.Status != *statusFilter {
			continue
		}
		children = append(children, edge.ChildThreadID)
	}
	sort.Strings(children)
	return children, nil
}

func (s *MemoryStore) ListThreadSpawnDescendants(rootThreadID string, statusFilter *ThreadSpawnEdgeStatus) ([]string, error) {
	if s == nil {
		return []string{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	childrenByParent := map[string][]ThreadSpawnEdge{}
	for _, edge := range s.edges {
		if statusFilter != nil && edge.Status != *statusFilter {
			continue
		}
		childrenByParent[edge.ParentThreadID] = append(childrenByParent[edge.ParentThreadID], edge)
	}
	for parent := range childrenByParent {
		sort.Slice(childrenByParent[parent], func(i int, j int) bool {
			return childrenByParent[parent][i].ChildThreadID < childrenByParent[parent][j].ChildThreadID
		})
	}
	queue := append([]ThreadSpawnEdge(nil), childrenByParent[rootThreadID]...)
	result := make([]string, 0)
	seen := map[string]bool{}
	for len(queue) > 0 {
		edge := queue[0]
		queue = queue[1:]
		if seen[edge.ChildThreadID] {
			continue
		}
		seen[edge.ChildThreadID] = true
		result = append(result, edge.ChildThreadID)
		queue = append(queue, childrenByParent[edge.ChildThreadID]...)
	}
	return result, nil
}
