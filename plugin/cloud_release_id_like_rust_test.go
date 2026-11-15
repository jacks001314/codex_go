package plugin

import "testing"

// TestCloudSourceLocationsCarryReleaseIDsLikeRust mirrors Rust #61393: cloud
// source locations may pin an opaque release ID, and it participates in the
// location identity used while merging executor installations.
func TestCloudSourceLocationsCarryReleaseIDsLikeRust(t *testing.T) {
	remote := PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: "plugin_remote"}
	cloud := &PluginCatalog{Entries: []PluginCatalogEntry{{
		ID:      remote,
		Version: "1.2.3",
		Locations: []PluginSourceLocation{{
			Kind:        PluginSourceCloud,
			ResourceURI: "plugin://plugin_remote",
			ReleaseID:   "release-1",
		}},
	}}}
	cloned := cloud.Clone()
	if got := cloned.Entries[0].Locations[0].ReleaseID; got != "release-1" {
		t.Fatalf("cloned release ID = %q, want release-1", got)
	}

	executor := &PluginCatalog{Entries: []PluginCatalogEntry{{
		ID:      remote,
		Version: "1.2.3",
		Locations: []PluginSourceLocation{{
			Kind:          PluginSourceExecutor,
			EnvironmentID: "executor-a",
			PluginID:      "remote@local",
			Root:          "/plugins/remote",
		}},
	}}}
	merged := MergePluginCatalogs(executor, cloud)
	locations := merged.Entries[0].Locations
	if len(locations) != 2 || locations[0].ReleaseID != "release-1" || locations[1].Kind != PluginSourceExecutor {
		t.Fatalf("merged locations = %#v, want the cloud release pinned before the executor installation", locations)
	}
}
