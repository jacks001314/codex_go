package state

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// UpsertThreadSpawnEdge persists the directional parent-child relationship.
// The child id is the identity of an edge, matching the Rust state schema.
func (r *StateRuntime) UpsertThreadSpawnEdge(ctx context.Context, parentThreadID string, childThreadID string, status string) error {
	if err := r.requireStateDB(); err != nil {
		return err
	}
	parentThreadID, childThreadID, status, err := normalizeThreadSpawnEdge(parentThreadID, childThreadID, status)
	if err != nil {
		return err
	}
	_, err = r.stateDB.ExecContext(nonNilContext(ctx), `
INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id, status)
VALUES (?, ?, ?)
ON CONFLICT(child_thread_id) DO UPDATE SET
    parent_thread_id = excluded.parent_thread_id,
    status = excluded.status`, parentThreadID, childThreadID, status)
	if err != nil {
		return fmt.Errorf("upsert thread spawn edge %s -> %s: %w", parentThreadID, childThreadID, err)
	}
	return nil
}

// SetThreadSpawnEdgeStatus updates an edge when it exists and is otherwise a no-op.
func (r *StateRuntime) SetThreadSpawnEdgeStatus(ctx context.Context, childThreadID string, status string) error {
	if err := r.requireStateDB(); err != nil {
		return err
	}
	childThreadID = strings.TrimSpace(childThreadID)
	status = strings.TrimSpace(status)
	if childThreadID == "" {
		return errors.New("child thread id is required")
	}
	if status == "" {
		return errors.New("thread spawn edge status is required")
	}
	if _, err := r.stateDB.ExecContext(nonNilContext(ctx), `UPDATE thread_spawn_edges SET status = ? WHERE child_thread_id = ?`, status, childThreadID); err != nil {
		return fmt.Errorf("set thread spawn edge status for %s: %w", childThreadID, err)
	}
	return nil
}

// ListThreadSpawnChildren returns direct children ordered by thread id.
// A nil status includes rows carrying status values introduced by newer clients.
func (r *StateRuntime) ListThreadSpawnChildren(ctx context.Context, parentThreadID string, status *string) ([]string, error) {
	if err := r.requireStateDB(); err != nil {
		return nil, err
	}
	parentThreadID = strings.TrimSpace(parentThreadID)
	if parentThreadID == "" {
		return nil, errors.New("parent thread id is required")
	}
	query := `SELECT child_thread_id FROM thread_spawn_edges WHERE parent_thread_id = ?`
	args := []any{parentThreadID}
	if status != nil {
		value := strings.TrimSpace(*status)
		if value == "" {
			return nil, errors.New("thread spawn edge status is required")
		}
		query += ` AND status = ?`
		args = append(args, value)
	}
	query += ` ORDER BY child_thread_id`
	rows, err := r.stateDB.QueryContext(nonNilContext(ctx), query, args...)
	if err != nil {
		return nil, fmt.Errorf("list children for thread %s: %w", parentThreadID, err)
	}
	defer rows.Close()
	children := make([]string, 0)
	for rows.Next() {
		var child string
		if err := rows.Scan(&child); err != nil {
			return nil, fmt.Errorf("scan child for thread %s: %w", parentThreadID, err)
		}
		children = append(children, child)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate children for thread %s: %w", parentThreadID, err)
	}
	return children, nil
}

// ListThreadSpawnDescendants returns descendants breadth-first, ordering every
// depth globally by thread id. The seen set makes corrupted cyclic graphs safe.
func (r *StateRuntime) ListThreadSpawnDescendants(ctx context.Context, rootThreadID string, status *string) ([]string, error) {
	if err := r.requireStateDB(); err != nil {
		return nil, err
	}
	rootThreadID = strings.TrimSpace(rootThreadID)
	if rootThreadID == "" {
		return nil, errors.New("root thread id is required")
	}
	ctx = nonNilContext(ctx)
	frontier := []string{rootThreadID}
	seen := map[string]struct{}{rootThreadID: {}}
	descendants := make([]string, 0)
	for len(frontier) > 0 {
		next := make([]string, 0)
		for _, parent := range frontier {
			children, err := r.ListThreadSpawnChildren(ctx, parent, status)
			if err != nil {
				return nil, err
			}
			for _, child := range children {
				if _, exists := seen[child]; exists {
					continue
				}
				seen[child] = struct{}{}
				next = append(next, child)
			}
		}
		sort.Strings(next)
		descendants = append(descendants, next...)
		frontier = next
	}
	return descendants, nil
}

func (r *StateRuntime) requireStateDB() error {
	if r == nil || r.stateDB == nil {
		return errors.New("state runtime is nil")
	}
	return nil
}

func normalizeThreadSpawnEdge(parentThreadID string, childThreadID string, status string) (string, string, string, error) {
	parentThreadID = strings.TrimSpace(parentThreadID)
	childThreadID = strings.TrimSpace(childThreadID)
	status = strings.TrimSpace(status)
	if parentThreadID == "" {
		return "", "", "", errors.New("parent thread id is required")
	}
	if childThreadID == "" {
		return "", "", "", errors.New("child thread id is required")
	}
	if status == "" {
		return "", "", "", errors.New("thread spawn edge status is required")
	}
	return parentThreadID, childThreadID, status, nil
}
