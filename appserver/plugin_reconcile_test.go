package appserver

import (
	"testing"

	"codex_go/plugin"
)

func TestPluginReconcileSignatureDetectsCapabilityChanges(t *testing.T) {
	base := plugin.PluginDetail{
		Summary:    plugin.PluginSummary{ID: "acme/weather", Enabled: true},
		MCPServers: []string{"weather"},
	}
	changed := base
	changed.Hooks = []plugin.PluginHookSummary{{Key: "post_tool_use"}}
	if pluginReconcileSignature(base) == pluginReconcileSignature(changed) {
		t.Fatal("pluginReconcileSignature should differ when hooks change")
	}
}

func TestRemoteInstalledPluginSyncFailuresSnapshotAndClear(t *testing.T) {
	clearRemoteInstalledPluginSyncFailures()
	recordRemoteInstalledPluginMaterializationFailure("acme/weather")
	recordRemoteInstalledPluginMaterializationFailure("acme/weather")
	failedRemote, failedMaterialization := takeRemoteInstalledPluginSyncFailures()
	if len(failedRemote) != 0 {
		t.Fatalf("failedRemote = %#v", failedRemote)
	}
	if len(failedMaterialization) != 2 || failedMaterialization[0] != "acme/weather" {
		t.Fatalf("failedMaterialization = %#v", failedMaterialization)
	}
	failedRemote, failedMaterialization = takeRemoteInstalledPluginSyncFailures()
	if len(failedRemote) != 0 || len(failedMaterialization) != 0 {
		t.Fatalf("second take = %#v / %#v", failedRemote, failedMaterialization)
	}
}
