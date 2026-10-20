package prompt

import (
	"strings"
	"testing"
)

func TestSkillAliasesRemainAppendOnlyWithinAScope(t *testing.T) {
	scope := "thread-alias-stability"
	first := RenderAvailableSkillsWithOptions([]InstructionsSkillMetadata{{
		Name: "alpha", Description: "a", Path: "C:/roots/a/alpha/SKILL.md", Root: "C:/roots/a", Scope: "user",
	}}, AvailableSkillsRenderOptions{
		Budget:     SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20},
		AliasScope: scope,
	})
	second := RenderAvailableSkillsWithOptions([]InstructionsSkillMetadata{{
		Name: "alpha", Description: "a", Path: "C:/roots/a/alpha/SKILL.md", Root: "C:/roots/a", Scope: "user",
	}, {
		Name: "beta", Description: "b", Path: "C:/roots/b/beta/SKILL.md", Root: "C:/roots/b", Scope: "user",
	}}, AvailableSkillsRenderOptions{
		Budget:     SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20},
		AliasScope: scope,
	})
	if first == nil || second == nil {
		t.Fatal("catalog did not render")
	}
	if !strings.Contains(strings.Join(second.SkillRootLines, "\n"), "- `r0` = `C:/roots/a`") {
		t.Fatalf("existing root was renumbered: %#v", second.SkillRootLines)
	}
	if !strings.Contains(strings.Join(second.SkillRootLines, "\n"), "- `r1` = `C:/roots/b`") {
		t.Fatalf("new root did not append: %#v", second.SkillRootLines)
	}
	if !strings.Contains(strings.Join(second.SkillLines, "\n"), "r0/alpha/SKILL.md") {
		t.Fatalf("existing skill alias changed: %#v", second.SkillLines)
	}
}

func TestCompleteCloudCatalogDisablesUnmatchedRemoteExecutorSkills(t *testing.T) {
	host, executor := RenderCombinedAvailableSkills([]InstructionsSkillMetadata{{
		Name: "host-skill", Description: "host", Path: "C:/host/SKILL.md", Root: "C:/host", Scope: "user",
	}}, []InstructionsSkillMetadata{
		{
			Name: "plugin:review", Description: "cloud", Path: "https://cloud/review/SKILL.md",
			LocatorKind: "cloud package", AuthorityKind: "cloud", RemotePluginID: "remote-1", PackageID: "cloud://review",
		},
		{
			Name: "plugin:review", Description: "executor", Path: "skill://exec/review/SKILL.md",
			LocatorKind: "executor package", AuthorityKind: "executor", RemotePluginID: "remote-1", PackageID: "skill://exec/review",
		},
		{
			Name: "plugin:other", Description: "executor", Path: "skill://exec/other/SKILL.md",
			LocatorKind: "executor package", AuthorityKind: "executor", RemotePluginID: "remote-2", PackageID: "skill://exec/other",
		},
	}, AvailableSkillsRenderOptions{
		Budget:              SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20},
		CloudSkillsComplete: true,
		AliasScope:          "thread-cloud-complete",
	})
	if host == nil || !strings.Contains(host.Body, "host-skill") {
		t.Fatalf("host catalog = %#v", host)
	}
	if executor == nil || !strings.Contains(executor.Body, "plugin:review") {
		t.Fatalf("cloud skill was not retained: %#v", executor)
	}
	if strings.Contains(executor.Body, "other") {
		t.Fatalf("unmatched remote executor skill was not disabled:\n%s", executor.Body)
	}
}
