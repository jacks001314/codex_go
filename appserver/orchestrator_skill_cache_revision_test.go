package appserver

import (
	"testing"

	"codex_go/config"
	"codex_go/mcp"
)

// Mirrors Rust #46033: orchestrator skill caches survive ordinary runtime
// generations, are explicitly invalidated for plugin/config refreshes, and are
// isolated when the MCP service connection identity changes.
func TestOrchestratorSkillCacheResourceGenerationLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	service := mcp.NewMCPService(nil)
	firstKey, firstCache := router.orchestratorSkillCacheForRuntime("thread-1", service)
	sameKey, sameCache := router.orchestratorSkillCacheForRuntime("thread-1", service)
	if sameKey != firstKey || sameCache != firstCache {
		t.Fatalf("ordinary lookup replaced the cache: %q/%q %p/%p", firstKey, sameKey, firstCache, sameCache)
	}

	service.InvalidateResourceCaches()
	invalidatedKey, invalidatedCache := router.orchestratorSkillCacheForRuntime("thread-1", service)
	if invalidatedKey == firstKey || invalidatedCache == firstCache {
		t.Fatalf("resource invalidation reused the cache: %q %p", invalidatedKey, invalidatedCache)
	}
	repeatKey, repeatCache := router.orchestratorSkillCacheForRuntime("thread-1", service)
	if repeatKey != invalidatedKey || repeatCache != invalidatedCache {
		t.Fatalf("post-invalidation lookup replaced the cache: %q/%q", invalidatedKey, repeatKey)
	}

	service.Refresh()
	refreshedKey, refreshedCache := router.orchestratorSkillCacheForRuntime("thread-1", service)
	if refreshedKey == invalidatedKey || refreshedCache == invalidatedCache {
		t.Fatalf("runtime refresh reused the resource cache: %q %p", refreshedKey, refreshedCache)
	}

	replacement := mcp.NewMCPService(nil)
	replacementKey, replacementCache := router.orchestratorSkillCacheForRuntime("thread-1", replacement)
	if replacementKey == refreshedKey || replacementCache == refreshedCache {
		t.Fatalf("replacement service collided with the old cache: %q %p", replacementKey, replacementCache)
	}
}

// Mirrors Rust #55705: cached cloud skill contents belong to the same auth /
// Apps generation as the published catalog. Changing the resource generation
// (the auth change, config mutation and plugin refresh path) exposes an empty
// contents cache, and a late read that still holds the replaced generation can
// only write into that unreachable generation.
func TestOrchestratorSkillResourceCacheClearsOnAuthScopeChangeLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	service := mcp.NewMCPService(nil)
	_, cache := router.orchestratorSkillCacheForRuntime("thread-1", service)
	cache.resources["skill://demo/work\x00skill://demo/work/SKILL.md"] = "contents"
	cache.resourceBytes = len("contents")

	service.InvalidateResourceCaches()
	_, replacement := router.orchestratorSkillCacheForRuntime("thread-1", service)
	if replacement == cache {
		t.Fatal("auth scope change reused the cloud skill contents cache")
	}
	if len(replacement.resources) != 0 || replacement.resourceBytes != 0 {
		t.Fatalf("new generation kept contents: %#v (%d bytes)", replacement.resources, replacement.resourceBytes)
	}

	cache.resources["late"] = "late-contents"
	if _, ok := replacement.resources["late"]; ok {
		t.Fatal("late read populated the replacement generation")
	}
}

func TestOrchestratorSkillCacheSurvivesApprovalPolicyRefreshLikeRust(t *testing.T) {
	coordinator := newMCPRuntimeCoordinator()
	service := mcp.NewMCPService(nil)
	router := &RuntimeRouter{mcpRuntimes: coordinator}
	firstService := coordinator.serviceForThread(
		"thread-1",
		&config.Config{Values: map[string]any{"approval_policy": "on-request"}},
		0,
		"",
		func(*config.Config) *mcp.MCPService { return service },
		func(*mcp.MCPService, *config.Config) {},
	)
	firstKey, firstCache := router.orchestratorSkillCacheForRuntime("thread-1", firstService)

	secondService := coordinator.serviceForThread(
		"thread-1",
		&config.Config{Values: map[string]any{"approval_policy": "never"}},
		0,
		"",
		func(*config.Config) *mcp.MCPService { return service },
		func(*mcp.MCPService, *config.Config) {},
	)
	secondKey, secondCache := router.orchestratorSkillCacheForRuntime("thread-1", secondService)
	if secondService != firstService || secondKey != firstKey || secondCache != firstCache {
		t.Fatalf("approval-policy refresh dropped the resource cache: %q/%q %p/%p", firstKey, secondKey, firstCache, secondCache)
	}

	coordinator.invalidateThread("thread-1")
	invalidatedKey, invalidatedCache := router.orchestratorSkillCacheForRuntime("thread-1", secondService)
	if invalidatedKey == secondKey || invalidatedCache == secondCache {
		t.Fatalf("explicit runtime invalidation kept the resource cache: %q %p", invalidatedKey, invalidatedCache)
	}
}
