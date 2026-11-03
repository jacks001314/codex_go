package agentboard

// Tree-scoped discovery of the in-memory MCP message board.
//
// Rust parity: codex-rs/ext/agent-message-board/src/mcp.rs (#58973). The
// launcher starts one loopback endpoint per agent tree and keeps it alive while
// at least one participating thread remains.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// MessageBoardMCPDiscovery owns one in-memory MCP endpoint per agent tree.
type MessageBoardMCPDiscovery struct {
	mu    sync.Mutex
	trees map[string]*messageBoardMCPTree
}

type messageBoardMCPTree struct {
	handle  *InMemoryMessageBoardMCP
	threads map[string]bool
}

// AcquireThread registers threadID with the tree's endpoint, launching the
// endpoint and the tree board on first use. It is idempotent per thread.
func (d *MessageBoardMCPDiscovery) AcquireThread(ctx context.Context, tree string, threadID string, host Host) (string, string, error) {
	if d == nil {
		return "", "", errors.New("message-board discovery is unavailable")
	}
	tree = strings.TrimSpace(tree)
	threadID = strings.TrimSpace(threadID)
	if tree == "" || threadID == "" {
		return "", "", errors.New("message-board discovery requires a tree and thread")
	}
	if host == nil {
		return "", "", errors.New("message-board discovery requires a host")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.trees == nil {
		d.trees = map[string]*messageBoardMCPTree{}
	}
	entry := d.trees[tree]
	if entry == nil {
		handle, err := LaunchInMemoryMessageBoardMCP(ctx, InMemoryMessageBoardMCPOptions{})
		if err != nil {
			return "", "", err
		}
		if err := handle.AddBoard(tree, host); err != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = handle.Shutdown(shutdownCtx)
			return "", "", err
		}
		entry = &messageBoardMCPTree{handle: handle, threads: map[string]bool{}}
		d.trees[tree] = entry
	}
	entry.threads[threadID] = true
	return entry.handle.Endpoint(), entry.handle.AuthorizationHeader(), nil
}

// ReleaseThread removes one participating thread; the endpoint shuts down when
// the tree has no threads left.
func (d *MessageBoardMCPDiscovery) ReleaseThread(tree string, threadID string) {
	if d == nil {
		return
	}
	tree = strings.TrimSpace(tree)
	threadID = strings.TrimSpace(threadID)
	d.mu.Lock()
	entry := d.trees[tree]
	if entry == nil {
		d.mu.Unlock()
		return
	}
	delete(entry.threads, threadID)
	if len(entry.threads) > 0 {
		d.mu.Unlock()
		return
	}
	delete(d.trees, tree)
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = entry.handle.Shutdown(ctx)
}

// Close shuts down every tree endpoint.
func (d *MessageBoardMCPDiscovery) Close(ctx context.Context) error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	trees := d.trees
	d.trees = nil
	d.mu.Unlock()
	var firstErr error
	for _, entry := range trees {
		if entry == nil || entry.handle == nil {
			continue
		}
		if err := entry.handle.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// TreeCount reports how many live tree endpoints exist.
func (d *MessageBoardMCPDiscovery) TreeCount() int {
	if d == nil {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.trees)
}
