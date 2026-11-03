package tea

import (
	"testing"

	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: #42453 (c9fecd3fa0) request_permission_profiles,
// permission_popup_request_is_current, and invalidate_permission_discovery.
// A discovery response only reaches the popup that asked for it; account,
// thread, or modal changes make the outstanding request stale.

func currencyRecorderModel() (*Model, *permissionProfileRecorder) {
	recorder := &permissionProfileRecorder{profiles: []chatwidget.CustomPermissionProfile{{ID: "trusted", Allowed: true}}}
	return recorder.model(false), recorder
}

func TestPermissionDiscoveryAppliesToCurrentPopupLikeRust(t *testing.T) {
	model, _ := currencyRecorderModel()
	command := model.openPermissionsMenu()
	if command == nil {
		t.Fatal("discovery did not start")
	}
	message := command().(permissionProfilesLoadedMsg)
	if !model.permissionPopupRequestIsCurrent(message) {
		t.Fatal("the popup's own response was rejected as stale")
	}
	model.applyPermissionProfilesLoaded(message)
	if !model.permissionProfilesDiscovered || len(model.permissionProfiles) != 1 {
		t.Fatalf("catalog = %#v", model.permissionProfiles)
	}
}

func TestPermissionDiscoveryDropsResponseAfterInvalidateLikeRust(t *testing.T) {
	model, _ := currencyRecorderModel()
	command := model.openPermissionsMenu()
	message := command().(permissionProfilesLoadedMsg)

	// A thread, account, or working-directory change invalidates discovery.
	model.invalidatePermissionDiscovery()
	model.applyPermissionProfilesLoaded(message)

	if model.permissionProfilesDiscovered || len(model.permissionProfiles) != 0 {
		t.Fatalf("stale response rewrote the catalog: %#v", model.permissionProfiles)
	}
	if len(model.permissionItems) != 0 {
		t.Fatalf("stale response rewrote the popup rows: %#v", model.permissionItems)
	}
}

func TestPermissionDiscoveryDropsResponseAfterPopupCloseLikeRust(t *testing.T) {
	model, _ := currencyRecorderModel()
	command := model.openPermissionsMenu()
	message := command().(permissionProfilesLoadedMsg)

	model.closePermissionsModal()
	model.applyPermissionProfilesLoaded(message)

	if model.permissionProfilesDiscovered {
		t.Fatal("a closed popup accepted its late discovery response")
	}
}

func TestPermissionDiscoveryReusesOutstandingRequestLikeRust(t *testing.T) {
	model, recorder := currencyRecorderModel()
	first := model.openPermissionsMenu()
	second := model.openPermissionsMenu()
	if first == nil || second != nil {
		t.Fatalf("open requests: first=%v second=%v", first != nil, second != nil)
	}
	if recorder.lists != 0 {
		t.Fatalf("the loader ran before the command executed: %d", recorder.lists)
	}
	if model.permissionDiscoveryGeneration != 1 {
		t.Fatalf("generation = %d, want one outstanding request", model.permissionDiscoveryGeneration)
	}

	// Invalidate bumps the generation so the outstanding request cannot apply.
	before := model.permissionDiscoveryGeneration
	model.invalidatePermissionDiscovery()
	if model.permissionDiscoveryGeneration <= before {
		t.Fatal("invalidate did not advance the generation")
	}
}
