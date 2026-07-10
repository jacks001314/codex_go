package prompt

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderAvailableSkillsOrdersAndFilters(t *testing.T) {
	disabled := false
	skills := []InstructionsSkillMetadata{
		{Name: "user-skill", Scope: "user", Description: "User skill", Path: filepath.Join("repo", "user", "SKILL.md")},
		{Name: "repo-skill", Scope: "repo", Description: "Repo skill", Path: filepath.Join("repo", "repo", "SKILL.md")},
		{Name: "hidden-skill", Scope: "system", Description: "Hidden", Path: filepath.Join("repo", "hidden", "SKILL.md"), AllowImplicitInvocation: &disabled},
	}
	available := RenderAvailableSkills(skills, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1000})
	if available == nil {
		t.Fatalf("RenderAvailableSkills() = nil")
	}
	if available.Report.TotalCount != 2 || available.Report.IncludedCount != 2 {
		t.Fatalf("Report = %#v", available.Report)
	}
	if len(available.SkillLines) != 2 || !strings.Contains(available.SkillLines[0], "repo-skill") || !strings.Contains(available.SkillLines[1], "user-skill") {
		t.Fatalf("SkillLines = %#v", available.SkillLines)
	}
	if strings.Contains(available.Body, "hidden-skill") || !strings.Contains(available.Body, "## Skills") {
		t.Fatalf("Body = %q", available.Body)
	}
}

func TestRenderAvailableSkillsTruncatesDescriptionsBeforeOmitting(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{Name: "alpha", Scope: "repo", Description: "abcdef", Path: "/tmp/alpha/SKILL.md"},
		{Name: "beta", Scope: "repo", Description: "uvwxyz", Path: "/tmp/beta/SKILL.md"},
	}
	budget := SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 0}
	minimum := 0
	for _, line := range orderedSkillRenderLines(skills) {
		minimum += budget.cost(line.renderMinimum() + "\n")
	}
	budget.Limit = minimum + 4
	available := RenderAvailableSkills(skills, budget)
	if available == nil {
		t.Fatalf("RenderAvailableSkills() = nil")
	}
	if available.Report.IncludedCount != 2 || available.Report.OmittedCount != 0 || available.Report.TruncatedDescriptionChars == 0 {
		t.Fatalf("Report = %#v", available.Report)
	}
	if strings.Contains(strings.Join(available.SkillLines, "\n"), "abcdef") || strings.Contains(strings.Join(available.SkillLines, "\n"), "uvwxyz") {
		t.Fatalf("SkillLines were not truncated: %#v", available.SkillLines)
	}
}

func TestRenderAvailableSkillsTokenBudgetWarningMentionsPercentLikeRust(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{Name: "alpha", Scope: "repo", Description: "abcdef", Path: "/tmp/alpha/SKILL.md"},
		{Name: "beta", Scope: "repo", Description: "uvwxyz", Path: "/tmp/beta/SKILL.md"},
	}
	available := RenderAvailableSkills(skills, SkillMetadataBudget{Kind: SkillMetadataBudgetTokens, Limit: 1})
	if available == nil || available.WarningMessage == nil {
		t.Fatalf("RenderAvailableSkills() = %#v, want warning", available)
	}
	want := "Exceeded skills context budget of 2%. All skill descriptions were removed and 2 additional skills were not included in the model-visible skills list."
	if *available.WarningMessage != want {
		t.Fatalf("warning = %q, want %q", *available.WarningMessage, want)
	}
}

func TestDefaultSkillMetadataBudget(t *testing.T) {
	if got := DefaultSkillMetadataBudget(200000); got.Kind != SkillMetadataBudgetTokens || got.Limit != 4000 {
		t.Fatalf("DefaultSkillMetadataBudget(200000) = %#v", got)
	}
	if got := DefaultSkillMetadataBudget(0); got.Kind != SkillMetadataBudgetCharacters || got.Limit != DefaultSkillMetadataCharBudget {
		t.Fatalf("DefaultSkillMetadataBudget(0) = %#v", got)
	}
}
