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

func TestPluginReconcileSignatureDetectsVersionChanges(t *testing.T) {
	version := "1.0.0"
	base := plugin.PluginDetail{
		Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, Version: &version},
	}
	next := base
	nextVersion := "2.0.0"
	next.Summary.Version = &nextVersion
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when versions change")
	}
}

func TestPluginReconcileSignatureDetectsRemotePluginIDChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, RemotePluginID: "remote-1"}}
	next := base
	next.Summary.RemotePluginID = "remote-2"
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when remote plugin IDs change")
	}
}

func TestPluginReconcileSignatureDetectsInstallPolicyChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, InstallPolicy: plugin.InstallAllowed}}
	next := base
	next.Summary.InstallPolicy = plugin.InstallBlocked
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when install policy changes")
	}
}

func TestPluginReconcileSignatureDetectsDisabledReasonChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, DisabledReason: pluginDisabledReasonPtr(plugin.PluginDisabledByAdminReason)}}
	next := base
	next.Summary.DisabledReason = pluginDisabledReasonPtr(plugin.PluginPlanNotEligibleReason)
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when disabled reason changes")
	}
}

func TestPluginReconcileSignatureDetectsAvailabilityChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, Availability: plugin.PluginAvailable}}
	next := base
	next.Summary.Availability = plugin.PluginDisabledByAdmin
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when availability changes")
	}
}

func TestPluginReconcileSignatureDetectsAuthPolicyChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, AuthPolicy: plugin.AuthOnUse}}
	next := base
	next.Summary.AuthPolicy = plugin.AuthOnInstall
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when auth policy changes")
	}
}

func TestPluginReconcileSignatureDetectsDisplayNameTagChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, PluginDisplayNameTag: "Weather"}}
	next := base
	next.Summary.PluginDisplayNameTag = "Forecast"
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when display name tag changes")
	}
}

func TestPluginReconcileSignatureDetectsEnablementChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true}}
	next := base
	next.Summary.Enabled = false
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when enablement changes")
	}
}

func TestPluginReconcileSignatureDetectsSkillsChange(t *testing.T) {
	base := plugin.PluginDetail{Summary: plugin.PluginSummary{ID: "acme/weather", Enabled: true, HasSkills: false}}
	next := base
	next.Summary.HasSkills = true
	if pluginReconcileSignature(base) == pluginReconcileSignature(next) {
		t.Fatal("pluginReconcileSignature should differ when skills presence changes")
	}
}

func pluginDisabledReasonPtr(value plugin.PluginDisabledReason) *plugin.PluginDisabledReason {
	return &value
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
