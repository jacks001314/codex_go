package prompt

import "testing"

// Rust ext/skills/src/render_dedup.rs::entries_for_render: an executor row is
// gated by its own main-prompt environment, a repository-scoped host row is
// gated by the turn's primary environment, and every other row is ungated.
func TestSkillCatalogGrantMarksExecutorAndRepoHostRowsLikeRust(t *testing.T) {
	executorGranted := InstructionsSkillMetadata{
		Name: "executor-granted", Description: "Executor skill.", Path: "/skills/executor-granted/SKILL.md", LocatorKind: "file",
		AuthorityKind: "executor", AuthorityID: "env-1", EnvironmentID: "env-1", PackageID: "pkg", ResourceID: "executor-granted",
	}
	executorOther := InstructionsSkillMetadata{
		Name: "executor-other", Description: "Other environment skill.", Path: "/skills/executor-other/SKILL.md", LocatorKind: "file",
		AuthorityKind: "executor", AuthorityID: "env-2", EnvironmentID: "env-2", PackageID: "pkg", ResourceID: "executor-other",
	}
	repoHost := InstructionsSkillMetadata{
		Name: "repo-host", Description: "Repository skill.", Path: "/repo/.codex/skills/repo-host/SKILL.md", LocatorKind: "file",
		Scope: "repo", AuthorityKind: "host", AuthorityID: "host", PackageID: "pkg", ResourceID: "repo-host",
	}
	userHost := InstructionsSkillMetadata{
		Name: "user-host", Description: "User skill.", Path: "/home/user/.codex/skills/user-host/SKILL.md", LocatorKind: "file",
		Scope: "user", AuthorityKind: "host", AuthorityID: "host", PackageID: "pkg", ResourceID: "user-host",
	}
	skills := []InstructionsSkillMetadata{executorGranted, executorOther, repoHost, userHost}

	render := func(options AvailableSkillsRenderOptions) map[string]string {
		t.Helper()
		options.Budget = SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget}
		options.IncludeUsageInstructions = true
		available := RenderAvailableSkillsWithOptions(skills, options)
		if available == nil {
			t.Fatal("expected a rendered catalog")
		}
		grants := map[string]string{}
		for _, entry := range available.Entries {
			grants[entry.ID] = entry.Grant
		}
		return grants
	}

	grantedByPrimary := render(AvailableSkillsRenderOptions{
		PrimaryEnvironment:    "env-1",
		GrantedEnvironmentIDs: map[string]struct{}{"env-1": {}},
	})
	if got := grantedByPrimary[skillCatalogEntryID(executorGranted)]; got != SkillGrantActive {
		t.Fatalf("executor row in the granted environment = %q, want %q", got, SkillGrantActive)
	}
	if got := grantedByPrimary[skillCatalogEntryID(executorOther)]; got != "" {
		t.Fatalf("executor row in an ungranted environment = %q, want no grant", got)
	}
	if got := grantedByPrimary[skillCatalogEntryID(repoHost)]; got != SkillGrantActive {
		t.Fatalf("repository host row under a granted primary environment = %q, want %q", got, SkillGrantActive)
	}
	if got := grantedByPrimary[skillCatalogEntryID(userHost)]; got != "" {
		t.Fatalf("user host row = %q, want no grant", got)
	}

	grantedOtherPrimary := render(AvailableSkillsRenderOptions{
		PrimaryEnvironment:    "env-2",
		GrantedEnvironmentIDs: map[string]struct{}{"env-1": {}},
	})
	if got := grantedOtherPrimary[skillCatalogEntryID(repoHost)]; got != "" {
		t.Fatalf("repository host row follows the primary environment, got %q", got)
	}
	if got := grantedOtherPrimary[skillCatalogEntryID(executorGranted)]; got != SkillGrantActive {
		t.Fatalf("executor row keeps its own environment grant, got %q", got)
	}

	withoutGrants := render(AvailableSkillsRenderOptions{PrimaryEnvironment: "env-1"})
	for id, grant := range withoutGrants {
		if grant != "" {
			t.Fatalf("row %s = %q without any filesystem grant", id, grant)
		}
	}
}
