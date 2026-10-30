package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ConnectionLeaseCapability is the opt-in extension a pooled stdio server can
// negotiate to receive a host-owned resource lease per chat (Rust #57978).
const ConnectionLeaseCapability = "codex/connection-lease"

// ConnectionLeaseMetaKey is the tool-call and resource-read metadata key that
// carries the chat's lease id. The host owns the value and replaces any
// caller-supplied one.
const ConnectionLeaseMetaKey = "codex/connectionLease"

// connectionLeaseTimeout bounds lease open and release requests so a wedged
// helper cannot stall chat startup or unload (Rust LEASE_REQUEST_TIMEOUT).
const connectionLeaseTimeout = 5 * time.Second

// mcpStdioConnectionPool is the process-owned registry for compatible local
// stdio servers using MCP 2026-07-28 (Rust #57211). A connection is shared only
// after it negotiated the modern protocol and did not request client input while
// starting; legacy helpers and startup-interactive helpers stay private.
//
// Every chat holds a lease. Releasing a lease never interrupts another chat, and
// the last release shuts the helper down.
type mcpStdioConnectionPool struct {
	mu      sync.Mutex
	entries []*mcpPooledStdioConnection
}

type mcpPooledStdioConnection struct {
	key    string
	client *stdioClient
	refs   int
	// retired connections are no longer joinable, but chats that already
	// leased one keep it until their final release (Rust #57211).
	retired bool
	closed  bool
}

var sharedMCPStdioConnections = &mcpStdioConnectionPool{}

// mcpChatLease is one chat's server-side lease on a shared connection. It is
// opened lazily on the chat's first tool call or resource read, after the
// helper has negotiated its capabilities (Rust #57978).
type mcpChatLease struct {
	mu     sync.Mutex
	id     string
	opened bool
	err    error
}

// ensureOpen registers this chat with the helper once. Helpers without the
// extension keep an empty lease id and existing behavior.
func (l *mcpChatLease) ensureOpen(client *stdioClient) (string, error) {
	if l == nil {
		return "", nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.opened {
		return l.id, l.err
	}
	l.opened = true
	if client == nil || !client.supportsConnectionLease() {
		return "", nil
	}
	leaseID := newMCPConnectionLeaseID()
	if err := client.openConnectionLease(leaseID); err != nil {
		l.err = err
		return "", err
	}
	l.id = leaseID
	return l.id, nil
}

// ID reports the opened lease id, or an empty string.
func (l *mcpChatLease) ID() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.id
}

// acquire returns a shareable connection for key, creating one when none is
// ready, plus the chat's lease handle. The returned release function must be
// called exactly once when the caller stops using the connection.
func (p *mcpStdioConnectionPool) acquire(key string, create func() *stdioClient) (*stdioClient, *mcpChatLease, func()) {
	if p == nil {
		client := create()
		return client, nil, func() { _ = client.Close() }
	}
	p.mu.Lock()
	for _, entry := range p.entries {
		if entry == nil || entry.closed || entry.retired || entry.key != key || entry.refs <= 0 || entry.client == nil {
			continue
		}
		if entry.client.isClosed() || !entry.client.isShareable() {
			continue
		}
		entry.refs++
		p.mu.Unlock()
		lease := &mcpChatLease{}
		return entry.client, lease, func() { p.releaseChat(entry, entry.client, lease) }
	}
	client := create()
	entry := &mcpPooledStdioConnection{key: key, client: client, refs: 1}
	p.entries = append(p.entries, entry)
	p.mu.Unlock()
	lease := &mcpChatLease{}
	return client, lease, func() { p.releaseChat(entry, client, lease) }
}

// releaseChat cancels this chat's pending requests before telling the helper to
// drop its lease. A failed release leaves server-side state behind, so the
// connection leaves the pool (Rust #57978).
func (p *mcpStdioConnectionPool) releaseChat(entry *mcpPooledStdioConnection, client *stdioClient, lease *mcpChatLease) {
	if p == nil || entry == nil {
		return
	}
	if leaseID := lease.ID(); leaseID != "" && client != nil && !client.isClosed() {
		if err := client.releaseConnectionLease(leaseID); err != nil {
			p.retire(entry)
		}
	}
	p.release(entry)
}

func (p *mcpStdioConnectionPool) release(entry *mcpPooledStdioConnection) {
	if p == nil || entry == nil {
		return
	}
	p.mu.Lock()
	if entry.closed {
		p.mu.Unlock()
		return
	}
	if entry.refs > 0 {
		entry.refs--
	}
	last := entry.refs == 0
	if last {
		entry.closed = true
		p.removeLocked(entry)
	}
	p.mu.Unlock()
	if last {
		_ = entry.client.Close()
	}
}

// retire removes a connection from the join set so a forced refresh starts a
// fresh helper. Chats that already leased it keep serving until their final
// release, so the refresh never interrupts them (Rust #57211).
func (p *mcpStdioConnectionPool) retire(entry *mcpPooledStdioConnection) {
	if p == nil || entry == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry.closed || entry.retired {
		return
	}
	entry.retired = true
	p.removeLocked(entry)
}

// retireClient retires the pooled entry owned by client, if any.
func (p *mcpStdioConnectionPool) retireClient(client *stdioClient) {
	if p == nil || client == nil {
		return
	}
	p.mu.Lock()
	var entry *mcpPooledStdioConnection
	for _, candidate := range p.entries {
		if candidate != nil && candidate.client == client {
			entry = candidate
			break
		}
	}
	p.mu.Unlock()
	p.retire(entry)
}

// removeLocked drops a finished entry so a later chat starts a fresh helper.
func (p *mcpStdioConnectionPool) removeLocked(entry *mcpPooledStdioConnection) {
	kept := p.entries[:0]
	for _, candidate := range p.entries {
		if candidate == entry {
			continue
		}
		kept = append(kept, candidate)
	}
	p.entries = kept
}

// mcpStdioSharingEligible reports whether a chat may join the process-owned pool
// for this server (Rust #57211): the sharing feature must be enabled, the server
// must be a local stdio launch that opted into protocol discovery, and the
// reserved Apps server, read-only servers, and per-chat credential servers stay
// private.
func (s *MCPService) mcpStdioSharingEligible(name string, config *ServerConfig) bool {
	if s == nil || config == nil || !s.discoverLocalStdio {
		return false
	}
	if strings.TrimSpace(name) == CodexAppsServerName {
		return false
	}
	if !config.IsLocalEnvironment() || strings.TrimSpace(config.Command) == "" {
		return false
	}
	if config.RequiresReadOnlyTools || config.ApplyHTTPRequest != nil {
		return false
	}
	if config.EffectiveAuth() == ServerAuthEMAAuth {
		return false
	}
	return config.ProtocolMode == MCPProtocolDiscoverLocalStdio || config.ProtocolMode == MCPProtocol20260728
}

// mcpStdioPoolKey scopes sharing to connections that can serve another chat:
// the same helper, environment, client capabilities, startup budget and catalog
// limit.
func mcpStdioPoolKey(connectionKey string, config *ServerConfig) string {
	startup := time.Duration(0)
	catalogLimit := 0
	if config != nil {
		startup = config.StartupTimeout
		catalogLimit = config.CatalogItemLimit
	}
	return fmt.Sprintf("%s|sharing|startup=%d|catalog=%d", connectionKey, startup, catalogLimit)
}

// newMCPConnectionLeaseID mints a host-owned lease identifier.
func newMCPConnectionLeaseID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("lease-%d", time.Now().UnixNano())
	}
	return "lease-" + hex.EncodeToString(buffer)
}
