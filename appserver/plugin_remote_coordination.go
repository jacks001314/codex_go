package appserver

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"codex_go/auth"
)

// remoteInstalledPluginsAuthIdentity scopes remote plugin snapshots to the
// account that produced them.
type remoteInstalledPluginsAuthIdentity struct {
	AuthMode         string
	AccountID        string
	ChatGPTUserID    string
	ChatGPTPlanType  string
	WorkspaceAccount bool
}

func remoteInstalledPluginsAuthIdentityFromSnapshot(snapshot *auth.AuthDotJSON) remoteInstalledPluginsAuthIdentity {
	if snapshot == nil {
		return remoteInstalledPluginsAuthIdentity{}
	}
	planType := auth.ChatGPTPlanTypeRawFromAuth(snapshot)
	return remoteInstalledPluginsAuthIdentity{
		AuthMode:         snapshot.Mode(),
		AccountID:        auth.AccountIDFromAuthForRestrictions(snapshot),
		ChatGPTUserID:    auth.ChatGPTUserIDFromAuth(snapshot),
		ChatGPTPlanType:  planType,
		WorkspaceAccount: auth.PlanType(strings.ToLower(strings.TrimSpace(planType))).IsWorkspaceAccount(),
	}
}

func remoteInstalledPluginsAuthIdentityKey(identity remoteInstalledPluginsAuthIdentity) string {
	workspace := "0"
	if identity.WorkspaceAccount {
		workspace = "1"
	}
	return strings.Join([]string{
		identity.AuthMode,
		identity.AccountID,
		identity.ChatGPTUserID,
		identity.ChatGPTPlanType,
		workspace,
	}, "\x00")
}

type remoteInstalledPluginCoordinationState struct {
	// gate serializes installed-plugin bundle reconciliation with direct remote
	// plugin installs and uninstalls.
	gate sync.Mutex
	// generation changes whenever authentication changes. A sync captures it
	// before fetching and may publish only if it is still current.
	generation atomic.Uint64
}

var remoteInstalledPluginCoordinations = struct {
	sync.Mutex
	byCodexHome map[string]*remoteInstalledPluginCoordinationState
}{byCodexHome: map[string]*remoteInstalledPluginCoordinationState{}}

func remoteInstalledPluginCoordinator(codexHome string) *remoteInstalledPluginCoordinationState {
	key := filepath.Clean(strings.TrimSpace(codexHome))
	remoteInstalledPluginCoordinations.Lock()
	defer remoteInstalledPluginCoordinations.Unlock()
	state := remoteInstalledPluginCoordinations.byCodexHome[key]
	if state == nil {
		state = &remoteInstalledPluginCoordinationState{}
		remoteInstalledPluginCoordinations.byCodexHome[key] = state
	}
	return state
}

func tryAcquireRemoteInstalledPluginSync(codexHome string) (generation uint64, release func(), ok bool) {
	if strings.TrimSpace(codexHome) == "" {
		return 0, func() {}, false
	}
	state := remoteInstalledPluginCoordinator(codexHome)
	if !state.gate.TryLock() {
		return 0, nil, false
	}
	return state.generation.Load(), state.gate.Unlock, true
}

func acquireRemoteInstalledPluginCacheMutation(codexHome string) func() {
	if strings.TrimSpace(codexHome) == "" {
		return func() {}
	}
	state := remoteInstalledPluginCoordinator(codexHome)
	state.gate.Lock()
	return state.gate.Unlock
}

func advanceRemoteInstalledPluginCacheGeneration(codexHome string) {
	if strings.TrimSpace(codexHome) == "" {
		return
	}
	remoteInstalledPluginCoordinator(codexHome).generation.Add(1)
}

func (r *RuntimeRouter) advanceRemoteInstalledPluginCacheGeneration() {
	if r == nil || r.services.Config == nil {
		return
	}
	advanceRemoteInstalledPluginCacheGeneration(r.services.Config.CodexHome())
}

func (r *RuntimeRouter) clearRemoteInstalledPluginMetadataForAuthChange() {
	if r == nil || r.services.Plugins == nil {
		return
	}
	for _, marketplaceName := range []string{
		remoteInstalledGlobalMarketplace,
		remoteInstalledUserMarketplace,
		remoteInstalledWorkspaceMarketplace,
		remoteInstalledWorkspaceSharedMarketplace,
	} {
		r.services.Plugins.ReplaceInstalledRemotePlugins(marketplaceName, nil)
	}
}

func remoteInstalledPluginGenerationCurrent(codexHome string, generation uint64) bool {
	return remoteInstalledPluginCoordinator(codexHome).generation.Load() == generation
}
