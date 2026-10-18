package appserver

import (
	"strings"

	"codex_go/auth"
	"codex_go/plugin"
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

func tryAcquireRemoteInstalledPluginSync(codexHome string) (generation uint64, release func(), ok bool) {
	return plugin.TryAcquireRemoteInstalledPluginSync(codexHome)
}

func acquireRemoteInstalledPluginCacheMutation(codexHome string) func() {
	return plugin.AcquireRemoteInstalledPluginCacheMutation(codexHome)
}

func advanceRemoteInstalledPluginCacheGeneration(codexHome string) {
	plugin.AdvanceRemoteInstalledPluginCacheGeneration(codexHome)
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
	return plugin.RemoteInstalledPluginGenerationCurrent(codexHome, generation)
}
