package appserver

import (
	"testing"

	execserverclient "codex_go/execserver"
)

func TestDiscoveryV2SkillEntriesProjectInventoryWithoutHostReads(t *testing.T) {
	short := "short"
	remote := "remote-plugin-1"
	snapshots := []execserverclient.ExecutorCapabilityDiscoveryV2Snapshot{{
		EnvironmentID: "remote-env",
		Response: &execserverclient.DiscoverV2CapabilitiesResponse{
			Warnings: []string{"warning"},
			Plugins: []execserverclient.ExecutorPlugin{{
				ID:             "local-plugin",
				DisplayName:    "Local Plugin",
				RemotePluginID: &remote,
				Root:           "/opt/plugins/local",
				Skills: []execserverclient.ExecutorSkill{{
					Name:             "review",
					Description:      "Review code",
					ShortDescription: &short,
					Path:             "/opt/plugins/local/skills/review/SKILL.md",
					DiscoveryRoot:    &execserverclient.SkillDiscoveryRoot{ID: "local", Path: "/opt/plugins/local/skills"},
				}},
			}, {
				ID:   "disabled-plugin",
				Root: "/opt/plugins/disabled",
				Skills: []execserverclient.ExecutorSkill{{
					Name: "hidden", Path: "/opt/plugins/disabled/hidden/SKILL.md",
				}},
			}},
			Skills: []execserverclient.ExecutorSkill{{
				Name: "workspace", Description: "Workspace skill", Path: "/home/user/.codex/skills/workspace/SKILL.md",
			}},
		},
	}}
	entries := skillEntriesFromDiscoveryV2(snapshots, []string{"disabled-plugin"})
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	first := entries[0]
	if first.PluginID != "local-plugin" || first.RemotePluginID != "remote-plugin-1" || first.EnvironmentID != "remote-env" || first.ShortDescription != "short" {
		t.Fatalf("plugin entry = %#v", first)
	}
	if first.PackageID == "" || first.ResourceID == "" || first.AuthorityID != "discovery:remote-env" || first.AuthorityKind != "executor" {
		t.Fatalf("executor locator = %#v", first)
	}
	if entries[1].PluginID != "" || entries[1].Name != "workspace" {
		t.Fatalf("top-level entry = %#v", entries[1])
	}
	for _, entry := range entries {
		if entry.Name == "hidden" {
			t.Fatal("disabled plugin contributed a skill")
		}
	}
}
