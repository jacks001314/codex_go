package prompt

import (
	"strings"
	"testing"
)

func omitPolicySkills() []InstructionsSkillMetadata {
	return []InstructionsSkillMetadata{
		{Name: "alpha", Description: "Alpha does the first thing.", Path: "/workspace/skills/alpha/SKILL.md", LocatorKind: "file", Scope: "user"},
		{Name: "beta", Description: "Beta does the second thing.", Path: "/workspace/skills/beta/SKILL.md", LocatorKind: "file", Scope: "user"},
	}
}

// Rust #57500: omitting descriptions keeps names and locators, and adapts the
// catalog introduction and usage instructions.
func TestRenderAvailableSkillsOmitDescriptionsLikeRust(t *testing.T) {
	rendered := RenderAvailableSkillsWithOptions(omitPolicySkills(), AvailableSkillsRenderOptions{
		Budget:                   SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget},
		IncludeUsageInstructions: true,
		OmitDescriptions:         true,
	})
	if rendered == nil {
		t.Fatal("expected a catalog")
	}
	if !rendered.OmitDescriptions {
		t.Fatal("catalog should record the omitted-description policy")
	}
	for _, want := range []string{
		"- alpha: (file: /workspace/skills/alpha/SKILL.md)",
		"- beta: (file: /workspace/skills/beta/SKILL.md)",
		"Each entry includes a name and source locator.",
		"(name + source locator)",
		"matches a skill's name shown above",
	} {
		if !strings.Contains(rendered.Body, want) {
			t.Fatalf("catalog missing %q:\n%s", want, rendered.Body)
		}
	}
	for _, unwanted := range []string{"Alpha does the first thing.", "name, description, and", "description shown above"} {
		if strings.Contains(rendered.Body, unwanted) {
			t.Fatalf("catalog should not contain %q:\n%s", unwanted, rendered.Body)
		}
	}
}

// The omitted policy never spends budget on descriptions, so a catalog that
// must truncate descriptions still lists complete rows.
func TestOmitDescriptionsNeverTruncatesLikeRust(t *testing.T) {
	skills := omitPolicySkills()
	for i := range skills {
		skills[i].Description = strings.Repeat("long description ", 200)
	}
	budget := SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 130}
	withDescriptions := RenderAvailableSkillsWithOptions(skills, AvailableSkillsRenderOptions{Budget: budget})
	if withDescriptions == nil || withDescriptions.Report.TruncatedDescriptionChars == 0 {
		t.Fatalf("expected descriptions to be truncated, got %+v", withDescriptions)
	}
	omitted := RenderAvailableSkillsWithOptions(skills, AvailableSkillsRenderOptions{
		Budget:           budget,
		OmitDescriptions: true,
	})
	if omitted == nil {
		t.Fatal("expected a catalog")
	}
	if omitted.Report.IncludedCount != 2 || omitted.Report.OmittedCount != 0 {
		t.Fatalf("omitted descriptions should fit both skills, got %+v", omitted.Report)
	}
	if omitted.Report.TruncatedDescriptionChars != 0 {
		t.Fatalf("omitted descriptions cannot be truncated, got %+v", omitted.Report)
	}
}

// Rust #57500: alias budget accounting uses the policy-shortened introduction,
// so the rendered body charged against the budget matches the catalog text.
func TestOmitDescriptionsShortensBudgetBodyLikeRust(t *testing.T) {
	withDescriptions := renderAvailableSkillsBudgetBodyWithPolicy(nil, nil, skillCatalogPolicy{})
	omitted := renderAvailableSkillsBudgetBodyWithPolicy(nil, nil, skillCatalogPolicy{omitDescriptions: true})
	if !strings.Contains(withDescriptions, "name, description, and") {
		t.Fatalf("full policy body should name descriptions:\n%s", withDescriptions)
	}
	if !strings.Contains(omitted, "name and") || strings.Contains(omitted, "name, description, and") {
		t.Fatalf("omitted policy body should drop descriptions:\n%s", omitted)
	}
	if len(omitted) >= len(withDescriptions) {
		t.Fatalf("omitted budget body %d should be shorter than %d", len(omitted), len(withDescriptions))
	}
}

// Rust #57500: description-only edits do not change an entry fingerprint while
// descriptions are omitted, so a delta can ignore them.
func TestOmitDescriptionsFingerprintIgnoresDescriptionEditsLikeRust(t *testing.T) {
	budget := SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget}
	first := RenderAvailableSkillsWithOptions(omitPolicySkills(), AvailableSkillsRenderOptions{Budget: budget, OmitDescriptions: true})
	edited := omitPolicySkills()
	edited[0].Description = "Completely different description."
	second := RenderAvailableSkillsWithOptions(edited, AvailableSkillsRenderOptions{Budget: budget, OmitDescriptions: true})
	if len(first.Entries) != 2 || len(second.Entries) != 2 {
		t.Fatalf("expected two comparison entries each, got %d and %d", len(first.Entries), len(second.Entries))
	}
	if first.Entries[0].Fingerprint != second.Entries[0].Fingerprint {
		t.Fatal("description edits must not change the fingerprint while descriptions are omitted")
	}
	visible := RenderAvailableSkillsWithOptions(omitPolicySkills(), AvailableSkillsRenderOptions{Budget: budget})
	visibleEdited := RenderAvailableSkillsWithOptions(edited, AvailableSkillsRenderOptions{Budget: budget})
	if visible.Entries[0].Fingerprint == visibleEdited.Entries[0].Fingerprint {
		t.Fatal("description edits must change the fingerprint while descriptions are visible")
	}
}

// The comparison metadata records which alias root a row used and whether the
// row made it into the bounded catalog.
func TestSkillCatalogEntriesRecordRootsAndVisibilityLikeRust(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{Name: "alpha", Description: "Alpha.", Path: "/workspace/skills/alpha/SKILL.md", LocatorKind: "file", Scope: "user", Root: "/workspace/skills"},
	}
	rendered := RenderAvailableSkillsWithOptions(skills, AvailableSkillsRenderOptions{
		Budget:     SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget},
		AliasScope: "entries-root-test",
	})
	if rendered == nil || len(rendered.Entries) != 1 {
		t.Fatalf("expected one comparison entry, got %+v", rendered)
	}
	entry := rendered.Entries[0]
	if !entry.Shown {
		t.Fatal("entry should be shown")
	}
	if entry.RootName == "" || entry.RootValue != "/workspace/skills" {
		t.Fatalf("expected a root alias record, got %+v", entry)
	}
	if !strings.Contains(entry.Line, entry.RootName+"/alpha/SKILL.md") {
		t.Fatalf("entry line should use the aliased locator, got %q", entry.Line)
	}
}
