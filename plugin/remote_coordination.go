package plugin

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// RemoteInstalledPluginCoordination serializes installed-bundle
// reconciliation with direct remote plugin cache mutations. The state is keyed
// by Codex home so independently constructed services for one host share the
// same gate (Rust #42114 PluginsManager).
type RemoteInstalledPluginCoordination struct {
	gate       sync.Mutex
	generation atomic.Uint64
}

var remoteInstalledPluginCoordinations = struct {
	sync.Mutex
	byCodexHome map[string]*RemoteInstalledPluginCoordination
}{byCodexHome: map[string]*RemoteInstalledPluginCoordination{}}

// RemoteInstalledPluginCoordinator returns the shared coordination state for
// a Codex home.
func RemoteInstalledPluginCoordinator(codexHome string) *RemoteInstalledPluginCoordination {
	key := filepath.Clean(strings.TrimSpace(codexHome))
	remoteInstalledPluginCoordinations.Lock()
	defer remoteInstalledPluginCoordinations.Unlock()
	state := remoteInstalledPluginCoordinations.byCodexHome[key]
	if state == nil {
		state = &RemoteInstalledPluginCoordination{}
		remoteInstalledPluginCoordinations.byCodexHome[key] = state
	}
	return state
}

// TryAcquireRemoteInstalledPluginSync attempts to hold the reconciliation gate
// without blocking. The returned generation must still be current when the
// caller publishes a fetched snapshot.
func TryAcquireRemoteInstalledPluginSync(codexHome string) (generation uint64, release func(), ok bool) {
	if strings.TrimSpace(codexHome) == "" {
		return 0, func() {}, false
	}
	state := RemoteInstalledPluginCoordinator(codexHome)
	if !state.gate.TryLock() {
		return 0, nil, false
	}
	return state.generation.Load(), state.gate.Unlock, true
}

// AcquireRemoteInstalledPluginCacheMutation holds the shared cache-root gate
// for one install or uninstall. The caller releases it before unrelated
// analytics, OAuth, or runtime refresh work.
func AcquireRemoteInstalledPluginCacheMutation(codexHome string) func() {
	if strings.TrimSpace(codexHome) == "" {
		return func() {}
	}
	state := RemoteInstalledPluginCoordinator(codexHome)
	state.gate.Lock()
	return state.gate.Unlock
}

// AdvanceRemoteInstalledPluginCacheGeneration invalidates snapshots fetched
// before an authentication change.
func AdvanceRemoteInstalledPluginCacheGeneration(codexHome string) {
	if strings.TrimSpace(codexHome) == "" {
		return
	}
	RemoteInstalledPluginCoordinator(codexHome).generation.Add(1)
}

// RemoteInstalledPluginGenerationCurrent reports whether a snapshot captured
// at generation may still be published.
func RemoteInstalledPluginGenerationCurrent(codexHome string, generation uint64) bool {
	return RemoteInstalledPluginCoordinator(codexHome).generation.Load() == generation
}
