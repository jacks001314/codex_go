package appserver

import (
	"strings"
	"testing"

	promptctx "codex_go/prompt"
)

func deltaSkill(name string, description string, path string, root string) promptctx.InstructionsSkillMetadata {
	return promptctx.InstructionsSkillMetadata{
		Name:        name,
		Description: description,
		Path:        path,
		LocatorKind: "file",
		Scope:       "user",
		Root:        root,
	}
}

func renderDeltaCatalog(t *testing.T, budget promptctx.SkillMetadataBudget, omit bool, scope string, skills ...promptctx.InstructionsSkillMetadata) *promptctx.AvailableSkills {
	t.Helper()
	rendered := promptctx.RenderAvailableSkillsWithOptions(skills, promptctx.AvailableSkillsRenderOptions{
		Budget:                   budget,
		IncludeUsageInstructions: true,
		OmitDescriptions:         omit,
		AliasScope:               scope,
	})
	if rendered == nil {
		t.Fatal("expected a rendered catalog")
	}
	return rendered
}

func charBudget(limit int) promptctx.SkillMetadataBudget {
	return promptctx.SkillMetadataBudget{Kind: promptctx.SkillMetadataBudgetCharacters, Limit: limit}
}

// Rust #57262: the first catalog for a surface is a full listing wrapped in the
// surface heading.
func TestSkillCatalogDeltaInitialListingLikeRust(t *testing.T) {
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	available := renderDeltaCatalog(t, budget, false, "", deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""))
	result := buildSkillCatalogDelta("Host", available, true, nil, new(int), budget)
	for _, want := range []string{
		"\n## Host skills\n\n<skills_instructions>",
		"- alpha: Alpha skill. (file: /skills/alpha/SKILL.md)",
		"### How to use skills",
	} {
		if !strings.Contains(result.Body, want) {
			t.Fatalf("initial listing missing %q:\n%s", want, result.Body)
		}
	}
	if !result.Snapshot.InstructionsSent {
		t.Fatal("initial listing must record that usage instructions were sent")
	}
	if len(result.Snapshot.Entries) != 1 {
		t.Fatalf("expected one entry, got %+v", result.Snapshot.Entries)
	}
	for id, entry := range result.Snapshot.Entries {
		if !entry.Shown {
			t.Fatalf("entry %s should be shown", id)
		}
	}
	if skillCatalogUsageCost(result.Snapshot, budget) <= 0 {
		t.Fatal("initial listing must charge its rows against the budget")
	}
}

// Rust #57262: later turns append only new and edited skills.
func TestSkillCatalogDeltaPublishesOnlyNewAndEditedSkillsLikeRust(t *testing.T) {
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	first := renderDeltaCatalog(t, budget, false, "delta-new",
		deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""),
		deltaSkill("beta", "Beta skill.", "/skills/beta/SKILL.md", ""),
	)
	initial := buildSkillCatalogDelta("Host", first, true, nil, new(int), budget)

	second := renderDeltaCatalog(t, budget, false, "delta-new",
		deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""),
		deltaSkill("beta", "Beta skill, now edited.", "/skills/beta/SKILL.md", ""),
		deltaSkill("gamma", "Gamma skill.", "/skills/gamma/SKILL.md", ""),
	)
	remaining := budget.Limit - skillCatalogUsageCost(initial.Snapshot, budget)
	update := buildSkillCatalogDelta("Host", second, true, initial.Snapshot, &remaining, budget)
	if !strings.Contains(update.Body, "\n## Host skills update\n") || !strings.Contains(update.Body, skillCatalogSkillsHeading) {
		t.Fatalf("expected an update message, got:\n%s", update.Body)
	}
	if !strings.Contains(update.Body, "- gamma: Gamma skill.") || !strings.Contains(update.Body, "Beta skill, now edited.") {
		t.Fatalf("update should carry new and edited skills:\n%s", update.Body)
	}
	if strings.Contains(update.Body, "- alpha:") {
		t.Fatalf("update must not repeat unchanged skills:\n%s", update.Body)
	}
	if strings.Contains(update.Body, "### How to use skills") {
		t.Fatalf("an unchanged policy must not repeat usage instructions:\n%s", update.Body)
	}
}

// Rust #57262: removals emit nothing and do not refund budget.
func TestSkillCatalogDeltaRemovalIsSilentAndKeepsBudgetLikeRust(t *testing.T) {
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	first := renderDeltaCatalog(t, budget, false, "delta-remove",
		deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""),
		deltaSkill("beta", "Beta skill.", "/skills/beta/SKILL.md", ""),
	)
	initial := buildSkillCatalogDelta("Host", first, true, nil, new(int), budget)
	used := skillCatalogUsageCost(initial.Snapshot, budget)

	second := renderDeltaCatalog(t, budget, false, "delta-remove", deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""))
	remaining := budget.Limit - used
	update := buildSkillCatalogDelta("Host", second, true, initial.Snapshot, &remaining, budget)
	if update.Body != "" {
		t.Fatalf("a removal must stay silent, got:\n%s", update.Body)
	}
	if remaining != budget.Limit-used {
		t.Fatalf("a removal must not refund budget: remaining %d, want %d", remaining, budget.Limit-used)
	}
	if len(update.Snapshot.Entries) != 1 {
		t.Fatalf("removed entry should leave the inventory, got %+v", update.Snapshot.Entries)
	}
}

// Rust #57500: while descriptions are omitted, a description-only edit is
// invisible to the incremental catalog.
func TestSkillCatalogDeltaIgnoresDescriptionOnlyEditsWhenOmittedLikeRust(t *testing.T) {
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	first := renderDeltaCatalog(t, budget, true, "delta-omit", deltaSkill("alpha", "Old description.", "/skills/alpha/SKILL.md", ""))
	initial := buildSkillCatalogDelta("Host", first, true, nil, new(int), budget)

	second := renderDeltaCatalog(t, budget, true, "delta-omit", deltaSkill("alpha", "New description.", "/skills/alpha/SKILL.md", ""))
	remaining := budget.Limit - skillCatalogUsageCost(initial.Snapshot, budget)
	update := buildSkillCatalogDelta("Host", second, true, initial.Snapshot, &remaining, budget)
	if update.Body != "" {
		t.Fatalf("description-only edits must not publish while omitted, got:\n%s", update.Body)
	}

	visible := renderDeltaCatalog(t, budget, false, "delta-visible", deltaSkill("alpha", "Old description.", "/skills/alpha/SKILL.md", ""))
	visibleInitial := buildSkillCatalogDelta("Host", visible, true, nil, new(int), budget)
	visibleSecond := renderDeltaCatalog(t, budget, false, "delta-visible", deltaSkill("alpha", "New description.", "/skills/alpha/SKILL.md", ""))
	visibleRemaining := budget.Limit - skillCatalogUsageCost(visibleInitial.Snapshot, budget)
	visibleUpdate := buildSkillCatalogDelta("Host", visibleSecond, true, visibleInitial.Snapshot, &visibleRemaining, budget)
	if !strings.Contains(visibleUpdate.Body, "New description.") {
		t.Fatalf("visible description edits must publish, got:\n%s", visibleUpdate.Body)
	}
}

// Rust #57262: an update that introduces a new alias mapping carries the root
// table with it.
func TestSkillCatalogDeltaEmitsRootMappingsLikeRust(t *testing.T) {
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	first := renderDeltaCatalog(t, budget, false, "delta-roots",
		deltaSkill("alpha", "Alpha skill.", "/workspace/one/alpha/SKILL.md", "/workspace/one"),
	)
	initial := buildSkillCatalogDelta("Host", first, true, nil, new(int), budget)
	second := renderDeltaCatalog(t, budget, false, "delta-roots",
		deltaSkill("alpha", "Alpha skill.", "/workspace/one/alpha/SKILL.md", "/workspace/one"),
		deltaSkill("beta", "Beta skill.", "/workspace/two/beta/SKILL.md", "/workspace/two"),
	)
	remaining := budget.Limit - skillCatalogUsageCost(initial.Snapshot, budget)
	update := buildSkillCatalogDelta("Host", second, true, initial.Snapshot, &remaining, budget)
	if !strings.Contains(update.Body, "### Skill roots") || !strings.Contains(update.Body, "/workspace/two") {
		t.Fatalf("update should carry the new root mapping:\n%s", update.Body)
	}
	if strings.Contains(update.Body, "/workspace/one") {
		t.Fatalf("update should not repeat known roots:\n%s", update.Body)
	}
}

// Rust #57262: a row that does not fit the remaining budget is reported as
// omitted and remembered as not shown.
func TestSkillCatalogDeltaExhaustedBudgetReportsOmittedLikeRust(t *testing.T) {
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	first := renderDeltaCatalog(t, budget, false, "delta-budget", deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""))
	initial := buildSkillCatalogDelta("Host", first, true, nil, new(int), budget)
	second := renderDeltaCatalog(t, budget, false, "delta-budget",
		deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""),
		deltaSkill("beta", "Beta skill.", "/skills/beta/SKILL.md", ""),
	)
	remaining := 1
	update := buildSkillCatalogDelta("Host", second, true, initial.Snapshot, &remaining, budget)
	if update.Body != "" {
		t.Fatalf("no row fits the remaining budget here, got:\n%s", update.Body)
	}
	if update.Report.OmittedCount != 1 || update.Report.IncludedCount != 0 {
		t.Fatalf("expected one omitted row, got %+v", update.Report)
	}
	warning := promptctx.SkillRenderWarningWithPolicy(update.Report, budget, true)
	if warning == nil || !strings.Contains(*warning, "Some skills were not included") {
		t.Fatalf("incremental catalogs use the terse omission warning, got %v", warning)
	}
}

// A policy change refreshes the usage instructions even when no row changed.
func TestSkillCatalogDeltaRefreshesUsageOnPolicyChangeLikeRust(t *testing.T) {
	budget := charBudget(promptctx.DefaultSkillMetadataCharBudget)
	first := renderDeltaCatalog(t, budget, false, "delta-policy", deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""))
	initial := buildSkillCatalogDelta("Host", first, true, nil, new(int), budget)
	second := renderDeltaCatalog(t, budget, true, "delta-policy", deltaSkill("alpha", "Alpha skill.", "/skills/alpha/SKILL.md", ""))
	remaining := budget.Limit - skillCatalogUsageCost(initial.Snapshot, budget)
	update := buildSkillCatalogDelta("Host", second, true, initial.Snapshot, &remaining, budget)
	if !strings.Contains(update.Body, "### How to use skills") || !strings.Contains(update.Body, "(name + source locator)") {
		t.Fatalf("policy change should refresh usage instructions, got:\n%s", update.Body)
	}
	for _, want := range []string{
		"\n## Host skills update\n",
		skillCatalogSkillsHeading,
		"- alpha: (file: /skills/alpha/SKILL.md)",
		"\n### How to use skills\n" + promptctx.SkillUsageInstructions(false, true) + "\n",
	} {
		if !strings.Contains(update.Body, want) {
			t.Fatalf("usage refresh body missing %q:\n%s", want, update.Body)
		}
	}
	if strings.Contains(update.Body, "Alpha skill.") {
		t.Fatalf("omitted policy must not publish descriptions:\n%s", update.Body)
	}
	if !update.Snapshot.OmitDescriptions {
		t.Fatal("snapshot must record the new description policy")
	}
}
