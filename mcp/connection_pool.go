package mcp

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

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

// acquire returns a shareable connection for key, creating one when none is
// ready. The returned release function must be called exactly once when the
// caller stops using the connection.
func (p *mcpStdioConnectionPool) acquire(key string, create func() *stdioClient) (*stdioClient, func()) {
	if p == nil {
		client := create()
		return client, func() { _ = client.Close() }
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
		return entry.client, func() { p.release(entry) }
	}
	client := create()
	entry := &mcpPooledStdioConnection{key: key, client: client, refs: 1}
	p.entries = append(p.entries, entry)
	p.mu.Unlock()
	return client, func() { p.release(entry) }
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
