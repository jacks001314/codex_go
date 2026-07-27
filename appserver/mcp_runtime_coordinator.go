package appserver

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"codex_go/config"
	"codex_go/mcp"
)

// mcpRuntimeCoordinator owns the MCP service associated with each loaded
// thread. Rust keeps an independent MCP runtime inside every Session; the Go
// app-server mirrors that ownership here while retaining the process-wide
// service for threadless management RPC compatibility.
type mcpRuntimeCoordinator struct {
	mu             sync.Mutex
	bindings       map[string]*mcpRuntimeBinding
	inflight       map[string]*mcpRuntimeBuild
	threadEpoch    map[string]uint64
	lifecycleEpoch map[string]uint64
	prewarming     map[string]bool
	prewarmPending map[string]bool
	globalEpoch    uint64
	nextRevision   uint64
	closed         bool
}

type mcpRuntimeBinding struct {
	service      *mcp.MCPService
	fingerprint  string
	authRevision uint64
	revision     uint64
	dirty        bool
}

type mcpRuntimeBuild struct {
	ready chan struct{}
}

func newMCPRuntimeCoordinator() *mcpRuntimeCoordinator {
	return &mcpRuntimeCoordinator{
		bindings:       map[string]*mcpRuntimeBinding{},
		inflight:       map[string]*mcpRuntimeBuild{},
		threadEpoch:    map[string]uint64{},
		lifecycleEpoch: map[string]uint64{},
		prewarming:     map[string]bool{},
		prewarmPending: map[string]bool{},
	}
}

func (c *mcpRuntimeCoordinator) serviceForThread(
	threadID string,
	cfg *config.Config,
	authRevision uint64,
	newService func(*config.Config) *mcp.MCPService,
	updateService func(*mcp.MCPService, *config.Config),
) *mcp.MCPService {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" || newService == nil {
		return nil
	}
	fingerprint := mcpRuntimeConfigFingerprint(cfg)
	for {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil
		}
		if current := c.bindings[threadID]; current != nil && current.service != nil &&
			!current.dirty && current.fingerprint == fingerprint && current.authRevision == authRevision {
			c.mu.Unlock()
			return current.service
		}
		if build := c.inflight[threadID]; build != nil {
			ready := build.ready
			c.mu.Unlock()
			<-ready
			continue
		}
		build := &mcpRuntimeBuild{ready: make(chan struct{})}
		c.inflight[threadID] = build
		globalEpoch := c.globalEpoch
		threadEpoch := c.threadEpoch[threadID]
		lifecycleEpoch := c.lifecycleEpoch[threadID]
		c.mu.Unlock()

		c.mu.Lock()
		current := c.bindings[threadID]
		c.mu.Unlock()
		next := (*mcp.MCPService)(nil)
		authChanged := current != nil && current.authRevision != authRevision
		if current != nil && current.service != nil && !authChanged && updateService != nil {
			next = current.service
			updateService(next, cfg)
		} else {
			next = newService(cfg)
		}

		c.mu.Lock()
		delete(c.inflight, threadID)
		close(build.ready)
		if c.closed || c.lifecycleEpoch[threadID] != lifecycleEpoch {
			c.mu.Unlock()
			if next != nil {
				_ = next.Close()
			}
			return nil
		}
		if next == nil {
			c.mu.Unlock()
			return nil
		}
		previous := c.bindings[threadID]
		c.nextRevision++
		c.bindings[threadID] = &mcpRuntimeBinding{
			service:      next,
			fingerprint:  fingerprint,
			authRevision: authRevision,
			revision:     c.nextRevision,
			dirty:        c.globalEpoch != globalEpoch || c.threadEpoch[threadID] != threadEpoch,
		}
		c.mu.Unlock()
		if previous != nil && previous.service != nil && previous.service != next {
			_ = previous.service.Close()
		}
		return next
	}
}

func (c *mcpRuntimeCoordinator) bindingRevision(threadID string, service *mcp.MCPService) uint64 {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" || service == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	binding := c.bindings[threadID]
	if binding == nil || binding.service != service {
		return 0
	}
	return binding.revision
}

func (c *mcpRuntimeCoordinator) isCurrent(threadID string, service *mcp.MCPService) bool {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" || service == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	binding := c.bindings[threadID]
	return !c.closed && binding != nil && binding.service == service
}

// schedulePrewarm coalesces best-effort refresh requests for one thread. Rust
// uses a bounded per-session channel for the same reason: exact turn setup is
// the correctness path, while background work should only prepare the newest
// desired state without allowing an unbounded queue to grow.
func (c *mcpRuntimeCoordinator) schedulePrewarm(threadID string, run func()) {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" || run == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	if c.prewarming[threadID] {
		c.prewarmPending[threadID] = true
		c.mu.Unlock()
		return
	}
	c.prewarming[threadID] = true
	c.mu.Unlock()

	go func() {
		for {
			run()
			c.mu.Lock()
			if c.closed || !c.prewarmPending[threadID] {
				delete(c.prewarming, threadID)
				delete(c.prewarmPending, threadID)
				c.mu.Unlock()
				return
			}
			delete(c.prewarmPending, threadID)
			c.mu.Unlock()
		}
	}()
}

func (c *mcpRuntimeCoordinator) refreshAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.globalEpoch++
	services := make([]*mcp.MCPService, 0, len(c.bindings))
	for _, binding := range c.bindings {
		if binding != nil && binding.service != nil {
			binding.dirty = true
			services = append(services, binding.service)
		}
	}
	c.mu.Unlock()
	for _, service := range services {
		service.Refresh()
	}
}

func (c *mcpRuntimeCoordinator) setOpenAIFormElicitationEnabled(enabled bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	services := make([]*mcp.MCPService, 0, len(c.bindings))
	for _, binding := range c.bindings {
		if binding != nil && binding.service != nil {
			services = append(services, binding.service)
		}
	}
	c.mu.Unlock()
	for _, service := range services {
		service.SetOpenAIFormElicitationEnabled(enabled)
	}
}

func (c *mcpRuntimeCoordinator) invalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.globalEpoch++
	for _, binding := range c.bindings {
		if binding != nil {
			binding.dirty = true
		}
	}
	c.mu.Unlock()
}

func (c *mcpRuntimeCoordinator) invalidateThread(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" {
		return
	}
	c.mu.Lock()
	c.threadEpoch[threadID]++
	if binding := c.bindings[threadID]; binding != nil {
		binding.dirty = true
	}
	c.mu.Unlock()
}

func (c *mcpRuntimeCoordinator) closeThread(threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" {
		return nil
	}
	c.mu.Lock()
	c.lifecycleEpoch[threadID]++
	binding := c.bindings[threadID]
	delete(c.bindings, threadID)
	delete(c.prewarmPending, threadID)
	c.mu.Unlock()
	if binding == nil || binding.service == nil {
		return nil
	}
	return binding.service.Close()
}

func (c *mcpRuntimeCoordinator) close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	clear(c.prewarmPending)
	services := make([]*mcp.MCPService, 0, len(c.bindings))
	for threadID, binding := range c.bindings {
		if binding != nil && binding.service != nil {
			services = append(services, binding.service)
		}
		delete(c.bindings, threadID)
	}
	c.mu.Unlock()
	var firstErr error
	for _, service := range services {
		if err := service.Close(); firstErr == nil && err != nil {
			firstErr = err
		}
	}
	return firstErr
}

func mcpRuntimeConfigFingerprint(cfg *config.Config) string {
	if cfg == nil || cfg.Values == nil {
		return ""
	}
	values := map[string]any{}
	for _, key := range []string{
		"mcp_servers", "mcpServers", "apps", "features", "chatgpt_base_url",
		"chatgptBaseUrl", "apps_mcp_product_sku", "appsMcpProductSku",
		"available_environment", "availableEnvironment", "connector_ids", "connectorIds",
		"approval_policy", "approvals_reviewer", "permissions", "sandbox_policy",
	} {
		if value, ok := cfg.Values[key]; ok {
			values[key] = value
		}
	}
	data, err := json.Marshal(values)
	if err == nil {
		return string(data) + "|requirements=" + config.MCPRequirementsFingerprint(cfg.Requirements)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%v|requirements=%s", keys, config.MCPRequirementsFingerprint(cfg.Requirements))
}
