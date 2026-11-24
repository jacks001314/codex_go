package appserver

import (
	"context"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
)

func attachCloudCatalogMCPBinding(router *RuntimeRouter, threadID string, service *mcp.MCPService) {
	router.mcpRuntimes.mu.Lock()
	defer router.mcpRuntimes.mu.Unlock()
	router.mcpRuntimes.bindings[threadID] = &mcpRuntimeBinding{service: service}
}

func cloudCatalogMCPBindingDirty(router *RuntimeRouter, threadID string) bool {
	router.mcpRuntimes.mu.Lock()
	defer router.mcpRuntimes.mu.Unlock()
	binding := router.mcpRuntimes.bindings[threadID]
	return binding != nil && binding.dirty
}

func failedCloudCatalogMCPService() *mcp.MCPService {
	service := mcp.NewMCPService(nil)
	message := "startup failed"
	service.SetServer(mcp.MCPServerStatus{Name: "slow", State: mcp.MCPServerFailed, Error: &message})
	return service
}

func TestCloudCatalogPublicationQueuesMCPPrewarmLikeRust(t *testing.T) {
	provider := &countingPluginCatalogProvider{}
	router := newCloudCatalogTestRouter(t, provider)
	// Keep the asynchronous prewarm from replacing the test-owned binding.
	router.services.Config = nil
	threadID := "thread-cloud-prewarm"
	service := mcp.NewMCPService(nil)
	attachCloudCatalogMCPBinding(router, threadID, service)

	router.refreshCloudPluginCatalogForStep(context.Background(), &config.Config{}, threadID, "turn-1")
	if got := provider.count(); got != 1 {
		t.Fatalf("discovery calls = %d, want 1", got)
	}
	if !cloudCatalogMCPBindingDirty(router, threadID) {
		t.Fatal("published cloud catalog did not mark the MCP projection dirty")
	}
}

func TestCloudCatalogClearQueuesMCPPrewarmLikeRust(t *testing.T) {
	router := newCloudCatalogTestRouter(t, &countingPluginCatalogProvider{})
	router.services.Config = nil
	threadID := "thread-cloud-clear"
	attachCloudCatalogMCPBinding(router, threadID, mcp.NewMCPService(nil))
	router.refreshCloudPluginCatalogForStep(context.Background(), &config.Config{}, threadID, "turn-1")
	router.mcpRuntimes.mu.Lock()
	router.mcpRuntimes.bindings[threadID].dirty = false
	router.mcpRuntimes.mu.Unlock()

	router.refreshCloudPluginCatalogForTurn(context.Background(), nil, threadID, "turn-2")
	if !cloudCatalogMCPBindingDirty(router, threadID) {
		t.Fatal("clearing a published cloud plugin catalog did not mark the MCP projection dirty")
	}
}

func TestCloudCatalogAlreadyDisabledDoesNotQueueMCPPrewarmLikeRust(t *testing.T) {
	router := newCloudCatalogTestRouter(t, &countingPluginCatalogProvider{})
	threadID := "thread-cloud-already-clear"
	attachCloudCatalogMCPBinding(router, threadID, mcp.NewMCPService(nil))

	router.refreshCloudPluginCatalogForTurn(context.Background(), nil, threadID, "turn-1")
	if cloudCatalogMCPBindingDirty(router, threadID) {
		t.Fatal("an already empty cloud plugin catalog queued redundant MCP work")
	}
}

func TestCancelledCloudCatalogLeavesStartupRetryForNextLockHolderLikeRust(t *testing.T) {
	provider := &countingPluginCatalogProvider{err: context.Canceled}
	router := newCloudCatalogTestRouter(t, provider)
	threadID := "thread-cloud-cancelled"
	state := router.cloudCatalogs.forThread(threadID)

	router.refreshCloudPluginCatalogForStep(context.Background(), &config.Config{}, threadID, "turn-1")
	if !state.takeMCPStartupRetry() {
		t.Fatal("cancelled discovery consumed the startup retry check")
	}
}

func TestCloudCatalogStartupRetryRunsOnceForPersistentFailureLikeRust(t *testing.T) {
	router := newCloudCatalogTestRouter(t, &countingPluginCatalogProvider{})
	router.services.Config = nil
	threadID := "thread-cloud-retry"
	attachCloudCatalogMCPBinding(router, threadID, failedCloudCatalogMCPService())
	state := router.cloudCatalogs.forThread(threadID)
	state.queueMCPStartupRetry()

	router.finishCloudCatalogStartup(threadID, state)
	if !cloudCatalogMCPBindingDirty(router, threadID) {
		t.Fatal("eligible startup failure did not queue MCP reprojection")
	}
	router.mcpRuntimes.mu.Lock()
	router.mcpRuntimes.bindings[threadID].dirty = false
	router.mcpRuntimes.mu.Unlock()
	router.finishCloudCatalogStartup(threadID, state)
	if cloudCatalogMCPBindingDirty(router, threadID) {
		t.Fatal("persistent failure was retried more than once without a new catalog refresh")
	}
}

func TestCloudCatalogStartupRetrySkipsAuthenticationFailureLikeRust(t *testing.T) {
	router := newCloudCatalogTestRouter(t, &countingPluginCatalogProvider{})
	router.services.Config = nil
	threadID := "thread-cloud-auth"
	service := mcp.NewMCPService(nil)
	reason := "reauthenticationRequired"
	service.SetServer(mcp.MCPServerStatus{
		Name:          "docs",
		State:         mcp.MCPServerFailed,
		AuthStatus:    mcp.MCPAuthNotLoggedIn,
		FailureReason: &reason,
	})
	attachCloudCatalogMCPBinding(router, threadID, service)
	state := router.cloudCatalogs.forThread(threadID)
	state.queueMCPStartupRetry()

	router.finishCloudCatalogStartup(threadID, state)
	if cloudCatalogMCPBindingDirty(router, threadID) {
		t.Fatal("authentication failure was queued for generic startup retry")
	}
}
