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
