package app

import (
	"context"
	"testing"

	"codex_go/appserverdaemon"
)

// TestCloudPermissionDiscoveryUsesServerSelectedPermissionsLikeRust mirrors
// Rust #57267 permission_discovery.rs:95: a server-owned conversation reports
// that its permissions are server-selected without sending a discovery request.
func TestCloudPermissionDiscoveryUsesServerSelectedPermissionsLikeRust(t *testing.T) {
	if err := serverOwnedPermissionDiscoveryError(ThreadParamsModeCloud); err == nil ||
		err.Error() != "Cloud conversations use server-selected permissions." {
		t.Fatalf("cloud discovery error = %v", err)
	}
	for _, mode := range []ThreadParamsMode{ThreadParamsModeEmbedded, ThreadParamsModeRemote} {
		if err := serverOwnedPermissionDiscoveryError(mode); err != nil {
			t.Fatalf("mode %q discovery error = %v, want nil", mode, err)
		}
	}

	loader := interactiveRemoteListPermissionProfiles(context.Background(), appserverdaemon.NewCloudEndpoint())
	profiles, explicit, err := loader()
	if err == nil || err.Error() != "Cloud conversations use server-selected permissions." {
		t.Fatalf("loader error = %v", err)
	}
	if profiles != nil || explicit {
		t.Fatalf("loader returned profiles=%#v explicit=%v", profiles, explicit)
	}
}
