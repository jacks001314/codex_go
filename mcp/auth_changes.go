package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// MCPAuthChangeCapability is the experimental capability a stdio MCP server
// advertises to opt in to auth-change notifications. The client offers the same
// capability during initialization (Rust codex-mcp auth_changes::CAPABILITY,
// #43428).
const MCPAuthChangeCapability = "codex/auth-change"

// MCPAuthChangeNotification carries the current credential and ownership
// revisions without any credentials (Rust auth_changes::NOTIFICATION).
const MCPAuthChangeNotification = "notifications/codex/authChanged"

// mcpAuthChangeSendTimeout bounds each notification send, matching Rust's
// SEND_TIMEOUT.
const mcpAuthChangeSendTimeout = 5 * time.Second

// MCPAuthChangeState mirrors Rust's AuthChangeState as forwarded to MCP
// servers.
type MCPAuthChangeState struct {
	Generation      uint64 `json:"generation"`
	OwnerGeneration uint64 `json:"ownerGeneration"`
}

// MCPAuthChangeSource supplies the current auth revisions and a channel closed
// on each change. It is the Go equivalent of Rust's watch::Receiver<AuthChangeState>.
type MCPAuthChangeSource interface {
	// AuthChangeState returns the latest revisions.
	AuthChangeState() MCPAuthChangeState
	// AuthChanged returns a channel closed on the next credential change.
	AuthChanged() <-chan struct{}
}

// MCPAuthChangeSourceFunc adapts functions to MCPAuthChangeSource.
type MCPAuthChangeSourceFunc struct {
	State   func() MCPAuthChangeState
	Changed func() <-chan struct{}
}

func (f MCPAuthChangeSourceFunc) AuthChangeState() MCPAuthChangeState {
	if f.State == nil {
		return MCPAuthChangeState{}
	}
	return f.State()
}

func (f MCPAuthChangeSourceFunc) AuthChanged() <-chan struct{} {
	if f.Changed == nil {
		return nil
	}
	return f.Changed()
}

// mcpServerSupportsAuthChange reports whether the initialized server opted in
// to auth-change notifications via its experimental capabilities.
func mcpServerSupportsAuthChange(capabilities json.RawMessage) bool {
	if len(capabilities) == 0 {
		return false
	}
	var payload struct {
		Experimental map[string]json.RawMessage `json:"experimental"`
	}
	if err := json.Unmarshal(capabilities, &payload); err != nil {
		return false
	}
	_, ok := payload.Experimental[MCPAuthChangeCapability]
	return ok
}

// mcpAdvertiseAuthChangeCapability inserts the client-side experimental
// capability that asks the server to opt in.
func mcpAdvertiseAuthChangeCapability(capabilities map[string]any) {
	if capabilities == nil {
		return
	}
	experimental, _ := capabilities["experimental"].(map[string]any)
	if experimental == nil {
		experimental = map[string]any{}
		capabilities["experimental"] = experimental
	}
	experimental[MCPAuthChangeCapability] = map[string]any{}
}

// startMCPAuthChangeWatcher sends the initial auth-change notification and
// subscribes to later changes. It is a no-op unless the server opted in and the
// client has a source, matching Rust's auth_changes::start gate.
func (c *stdioClient) startMCPAuthChangeWatcher() {
	if c == nil || c.authChanges == nil {
		return
	}
	if !mcpServerSupportsAuthChange(c.serverCapabilitiesRaw()) {
		return
	}
	if err := c.sendMCPAuthChangeNotification(); err != nil {
		// Rust fails startup when the initial notification cannot be sent.
		c.failTransportFor(c.currentCommand(), err)
		return
	}
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		stop()
		return
	}
	if c.authChangeStop != nil {
		c.authChangeStop()
	}
	c.authChangeStop = stop
	c.mu.Unlock()

	cmd := c.currentCommand()
	go func() {
		changed := c.authChanges.AuthChanged()
		for {
			if changed == nil {
				// No watcher available: keep the initial notification and stop.
				return
			}
			select {
			case <-done:
				return
			case <-changed:
			}
			if err := c.sendMCPAuthChangeNotification(); err != nil {
				// Rust closes the connection when a follow-up send fails.
				c.failTransportFor(cmd, err)
				return
			}
			changed = c.authChanges.AuthChanged()
		}
	}()
}

func (c *stdioClient) stopAuthChangeWatcherLocked() {
	if c == nil || c.authChangeStop == nil {
		return
	}
	stop := c.authChangeStop
	c.authChangeStop = nil
	stop()
}

func (c *stdioClient) sendMCPAuthChangeNotification() error {
	if c == nil || c.authChanges == nil {
		return nil
	}
	if c.isClosed() {
		return io.ErrClosedPipe
	}
	state := c.authChanges.AuthChangeState()
	frame := map[string]any{
		"jsonrpc": "2.0",
		"method":  MCPAuthChangeNotification,
		"params": map[string]any{
			"_meta":           map[string]any{},
			"generation":      state.Generation,
			"ownerGeneration": state.OwnerGeneration,
		},
	}
	written := make(chan error, 1)
	go func() { written <- c.writeFrame(frame) }()
	select {
	case err := <-written:
		return err
	case <-time.After(mcpAuthChangeSendTimeout):
		return fmt.Errorf("MCP auth change notification timed out after %s", mcpAuthChangeSendTimeout)
	}
}
