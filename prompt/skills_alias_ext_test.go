package prompt

import (
	"strings"
	"testing"
)

func TestExtensionSkillAliasRootDerivesFromLocator(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"skill://root-1/skills/foo/SKILL.md", "skill://root-1/skills"},
		{"file:///repo/.codex/skills/bar", "file:///repo/.codex"},
		{"https://example.com/skills/baz", "https://example.com/skills"},
	}
	for _, tc := range cases {
		if got := extensionSkillAliasRoot(tc.path); got != tc.want {
			t.Fatalf("extensionSkillAliasRoot(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestBuildExtensionAliasPlanUsesEPrefix(t *testing.T) {
	lines := []skillRenderLine{
		{name: "foo", path: "skill://root-1/skills/foo/SKILL.md", locatorKind: "file"},
		{name: "bar", path: "skill://root-1/skills/bar/SKILL.md", locatorKind: "file"},
	}
	budget := SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 100}
	plan, ok := buildExtensionAliasPlan(lines, budget)
	if !ok {
		t.Fatal("buildExtensionAliasPlan() = false, want true")
	}
	if len(plan.rootLines) != 1 || !strings.HasPrefix(plan.rootLines[0], "- `e0`") {
		t.Fatalf("rootLines = %+v, want single e0 alias", plan.rootLines)
	}
	aliased := applySkillAliases(lines, plan)
	for _, line := range aliased {
		if !strings.HasPrefix(line.path, "e0/") {
			t.Fatalf("aliased path = %q, want e0/ prefix", line.path)
		}
		if strings.Contains(line.path, "skill://") {
			t.Fatalf("aliased path = %q, still contains full locator", line.path)
		}
	}
}

func TestBuildExtensionAliasPlanLongestPrefixMatching(t *testing.T) {
	lines := []skillRenderLine{
		{name: "a", path: "skill://root-1/skills/foo/SKILL.md"},
		{name: "b", path: "skill://root-2/other/bar/SKILL.md"},
	}
	plan, ok := buildExtensionAliasPlan(lines, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 500})
	if !ok || len(plan.rootAliases) != 2 {
		t.Fatalf("plan = %+v, want two roots", plan)
	}
	if plan.rootAliases["skill://root-1/skills"] != "e0" || plan.rootAliases["skill://root-2/other"] != "e1" {
		t.Fatalf("aliases = %+v", plan.rootAliases)
	}
}

func TestExtensionAliasDoesNotApplyWithoutRoot(t *testing.T) {
	lines := []skillRenderLine{{name: "a", path: "short/path.md"}}
	if plan, ok := buildExtensionAliasPlan(lines, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 500}); ok {
		t.Fatalf("plan = %+v, want none for short path", plan)
	}
}
