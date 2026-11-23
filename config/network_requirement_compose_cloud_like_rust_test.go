package config

import (
	"strings"
	"testing"
)

// TestCloudNetworkRequirementsComposeAcrossFragmentsLikeRust mirrors Rust #62882:
// managed fragments using different network table spellings compose their domain
// rules instead of the highest layer replacing the whole table.
func TestCloudNetworkRequirementsComposeAcrossFragmentsLikeRust(t *testing.T) {
	requirements, err := applyCloudConfigBundle(map[string]any{}, nil, CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "org", Name: "Org", Contents: "[permissions.network]\nallowed_domains = ['base.test']\ndenied_domains = ['blocked.test']"},
			{ID: "device", Name: "Device", Contents: "[network_proxy]\nallowed_domains = ['extra.test']\ndenied_domains = ['retired.test']"},
		}},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("applyCloudConfigBundle() error = %v", err)
	}
	if requirements == nil || requirements.Network == nil {
		t.Fatal("composed network requirements are missing")
	}
	want := map[string]NetworkPermission{
		"base.test":    NetworkAllow,
		"blocked.test": NetworkDeny,
	}
	if len(requirements.Network.Domains) != len(want) {
		t.Fatalf("domains = %#v, want %#v", requirements.Network.Domains, want)
	}
	for pattern, permission := range want {
		if requirements.Network.Domains[pattern] != permission {
			t.Fatalf("domains = %#v, want %#v", requirements.Network.Domains, want)
		}
	}
}

// TestCloudNetworkRequirementsComposeWithLocalLayerLikeRust pins that the
// machine-owned requirements layer contributes its grants and denials while the
// cloud layer's higher-priority lists replace like lists.
func TestCloudNetworkRequirementsComposeWithLocalLayerLikeRust(t *testing.T) {
	local, err := ParseRequirementsTOML([]byte("[network_proxy]\nallowed_domains = ['local.test']\ndenied_domains = ['shared.test']"))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	merged, _, err := applyCloudConfigBundleWithFilesystemSource(map[string]any{}, local, CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{{
			ID: "org", Name: "Org", Contents: "[permissions.network]\nallowed_domains = ['cloud.test']",
		}}},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("applyCloudConfigBundleWithFilesystemSource() error = %v", err)
	}
	if merged == nil || merged.Network == nil {
		t.Fatal("composed network requirements are missing")
	}
	want := map[string]NetworkPermission{
		"cloud.test":  NetworkAllow,
		"shared.test": NetworkDeny,
	}
	if len(merged.Network.Domains) != len(want) {
		t.Fatalf("domains = %#v, want %#v", merged.Network.Domains, want)
	}
	for pattern, permission := range want {
		if merged.Network.Domains[pattern] != permission {
			t.Fatalf("domains = %#v, want %#v", merged.Network.Domains, want)
		}
	}
}

// TestCloudNetworkRequirementsRejectMixedSocketSpellingsLikeRust pins the Unix
// socket restriction: rules that name paths still require one table spelling
// across sources.
func TestCloudNetworkRequirementsRejectMixedSocketSpellingsLikeRust(t *testing.T) {
	_, err := applyCloudConfigBundle(map[string]any{}, nil, CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "org", Name: "Org", Contents: "[network_proxy]\nenabled = true"},
			{ID: "device", Name: "Device", Contents: "[permissions.network.unix_sockets]\n'/tmp/managed.sock' = 'deny'"},
		}},
	}, t.TempDir())
	if err == nil {
		t.Fatal("expected a mixed socket spelling error")
	}
	if !strings.Contains(err.Error(), "Unix socket path rules") {
		t.Fatalf("error = %v", err)
	}
}

// TestCloudNetworkRequirementsRejectDisagreeingMirrorLikeRust pins that a
// fragment publishing both spellings must compose domain rules the same way.
func TestCloudNetworkRequirementsRejectDisagreeingMirrorLikeRust(t *testing.T) {
	_, err := applyCloudConfigBundle(map[string]any{}, nil, CloudConfigBundle{
		RequirementsTOML: CloudConfigRequirementsTOMLBundle{EnterpriseManaged: []CloudConfigFragment{
			{ID: "org", Name: "Org", Contents: "[permissions.network]\nallowed_domains = ['a.test']\ndenied_domains = ['b.test']"},
			{ID: "device", Name: "Device", Contents: "[network_proxy]\nallowed_domains = ['c.test']\n[permissions.network]\ndomains.'c.test' = 'allow'"},
		}},
	}, t.TempDir())
	if err == nil {
		t.Fatal("expected a mirrored domain rule error")
	}
	if !strings.Contains(err.Error(), "Device") {
		t.Fatalf("error = %v, want the Device fragment", err)
	}
}
