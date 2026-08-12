package prompt

import (
	"fmt"
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
	for _, want := range []string{
		SkillsInstructionsOpenTag,
		"## Skills",
		SkillsIntroWithAbsolutePaths,
		"### Available skills",
		"### How to use skills",
		"After deciding to use a skill, the main agent must read its `SKILL.md` completely",
		SkillsInstructionsCloseTag,
	} {
		if !strings.Contains(available.Body, want) {
			t.Fatalf("Body missing %q in:\n%s", want, available.Body)
		}
	}
	if strings.Contains(available.Body, "hidden-skill") {
		t.Fatalf("Body = %q", available.Body)
	}
}

func TestSkillAliasesFollowRootDiscoveryOrderLikeRust(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{Name: "alpha", Path: "C:/second/alpha/SKILL.md", Root: "C:/second", RootOrder: 1, HasRootOrder: true},
		{Name: "zulu", Path: "C:/first/zulu/SKILL.md", Root: "C:/first", RootOrder: 0, HasRootOrder: true},
	}
	lines := orderedSkillRenderLines(skills)
	if lines[0].name != "alpha" {
		t.Fatalf("render order changed = %#v", lines)
	}
	plan, ok := buildSkillAliasPlan(lines, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20})
	if !ok || len(plan.rootLines) != 2 || plan.rootLines[0] != "- `r0` = `C:/first`" || plan.rootLines[1] != "- `r1` = `C:/second`" {
		t.Fatalf("root aliases = %#v", plan)
	}
}

func TestRenderAvailableSkillsCustomResourceLocatorLikeRust(t *testing.T) {
	available := RenderAvailableSkills([]InstructionsSkillMetadata{{
		Name:        "private-search",
		Scope:       "custom",
		Description: "Search a private authority.",
		LocatorKind: "custom resource",
		LocatorPath: "private://catalog/search/SKILL.md",
	}}, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 2000})
	if available == nil || !strings.Contains(available.Body, "- private-search: Search a private authority. (custom resource: private://catalog/search/SKILL.md)") {
		t.Fatalf("custom resource catalog = %#v", available)
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

func TestRenderAvailableSkillsTokenTruncationWarningMentionsPercentLikeRust(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{Name: "long-skill", Scope: "repo", Description: strings.Repeat("a", 1000), Path: "/tmp/long/SKILL.md"},
	}
	budget := SkillMetadataBudget{Kind: SkillMetadataBudgetTokens}
	minimum := 0
	for _, line := range orderedSkillRenderLines(skills) {
		minimum += budget.cost(line.renderMinimum() + "\n")
	}
	budget.Limit = minimum + 1
	available := RenderAvailableSkills(skills, budget)
	if available == nil || available.WarningMessage == nil {
		t.Fatalf("RenderAvailableSkills() = %#v, want warning", available)
	}
	if *available.WarningMessage != SkillDescriptionTruncatedWarningWithPercent {
		t.Fatalf("warning = %q, want %q", *available.WarningMessage, SkillDescriptionTruncatedWarningWithPercent)
	}
}

func TestRenderAvailableSkillsPreservesDescriptionWhitespaceAndFileLabelLikeRust(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{Name: "env-skill", Scope: "environment", Description: "alpha\t  beta", Path: "environment://remote/skills/env-skill/SKILL.md"},
	}
	available := RenderAvailableSkills(skills, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1000})
	if available == nil || len(available.SkillLines) != 1 {
		t.Fatalf("RenderAvailableSkills() = %#v", available)
	}
	want := "- env-skill: alpha\t  beta (file: environment://remote/skills/env-skill/SKILL.md)"
	if available.SkillLines[0] != want {
		t.Fatalf("skill line = %q, want %q", available.SkillLines[0], want)
	}
}

func TestRenderAvailableSkillsUsesLocatorKindAndPathLikeRust(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{
			Name:        "env-skill",
			Scope:       "environment",
			Description: "Executor skill",
			Path:        "environment://remote/remote/skills/env-skill/SKILL.md",
			LocatorPath: "skill://executor/remote/skills/env-skill/SKILL.md",
			LocatorKind: "executor package",
			PackageID:   "skill://executor/remote/skills/env-skill",
		},
	}
	available := RenderAvailableSkills(skills, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1000})
	if available == nil || len(available.SkillLines) != 1 {
		t.Fatalf("RenderAvailableSkills() = %#v", available)
	}
	want := "- env-skill: Executor skill (executor package: skill://executor/remote/skills/env-skill)"
	if available.SkillLines[0] != want {
		t.Fatalf("skill line = %q, want %q", available.SkillLines[0], want)
	}
}

func TestRenderExtensionAvailableSkillsAddsDirectPackageReadInstructionsLikeRust(t *testing.T) {
	executor := []InstructionsSkillMetadata{
		{Name: "env-skill", Description: "Executor skill", Path: "skill://executor/remote/skills/env-skill/SKILL.md", LocatorKind: "executor package", PackageID: "skill://executor/remote/skills/env-skill"},
		{Name: "orch-skill", Description: "Orchestrator skill", Path: "skill://orchestrator/plugin/skills/orch-skill/SKILL.md", LocatorKind: "orchestrator package", PackageID: "skill://orchestrator/plugin/skills/orch-skill"},
	}
	available := RenderExtensionAvailableSkills(executor, false)
	if available == nil {
		t.Fatalf("extension available skills = nil")
	}
	for _, want := range []string{
		"Read a skill package directly with `skills.read({\"package\":\"<package>\"})`",
		"use `skills.list` to find it",
		"(executor package: skill://executor/remote/skills/env-skill)",
		"(orchestrator package: skill://orchestrator/plugin/skills/orch-skill)",
	} {
		if !strings.Contains(available.Body, want) {
			t.Fatalf("extension body missing %q in:\n%s", want, available.Body)
		}
	}
}

func TestRenderAvailableSkillsHostCatalogOmitsDirectPackageReadInstructionsLikeRust(t *testing.T) {
	host := []InstructionsSkillMetadata{
		{Name: "alpha", Scope: "repo", Path: "/tmp/skills/alpha/SKILL.md", Root: "/tmp/skills"},
	}
	available := RenderAvailableSkills(host, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20})
	if available == nil {
		t.Fatalf("RenderAvailableSkills() = nil")
	}
	if strings.Contains(available.Body, "Read a skill package directly with") {
		t.Fatalf("host catalog unexpectedly includes direct package read instructions:\n%s", available.Body)
	}
}

func TestSkillRenderLocatorPreservesLiteralBackslashesInPackageIDsLikeRust(t *testing.T) {
	pkg := `skill://executor/workspace/foo\bar/skills/demo`
	lines := extensionSkillRenderLines([]InstructionsSkillMetadata{{
		Name:        "demo",
		Description: "Description.",
		Path:        pkg + "/SKILL.md",
		LocatorKind: "executor package",
		PackageID:   pkg,
	}})
	if len(lines) != 1 {
		t.Fatalf("extension lines = %#v", lines)
	}
	if lines[0].path != pkg {
		t.Fatalf("rendered locator = %q, want literal backslash preserved: %q", lines[0].path, pkg)
	}
	plan, ok := buildExtensionAliasPlan(lines, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 500})
	if !ok {
		t.Fatalf("buildExtensionAliasPlan() = false, want true")
	}
	aliased := applySkillAliases(lines, plan)
	if len(aliased) != 1 || !strings.HasPrefix(aliased[0].path, "e0/") || !strings.Contains(aliased[0].path, `foo\bar`) {
		t.Fatalf("aliased path = %#v, want e0/ prefix with preserved backslash", aliased)
	}
}

func TestRenderAvailableSkillsOmitsAliasesWithoutBudgetPressureLikeRust(t *testing.T) {
	root := "/tmp/skills"
	skills := []InstructionsSkillMetadata{
		{Name: "alpha-skill", Scope: "repo", Path: root + "/alpha/SKILL.md", Root: root},
		{Name: "beta-skill", Scope: "repo", Path: root + "/beta/SKILL.md", Root: root},
	}

	available := RenderAvailableSkills(skills, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20})
	if available == nil {
		t.Fatalf("RenderAvailableSkills() = nil")
	}
	if len(available.SkillRootLines) != 0 {
		t.Fatalf("SkillRootLines = %#v, want none", available.SkillRootLines)
	}
	if strings.Contains(available.Body, "### Skill roots") {
		t.Fatalf("Body unexpectedly used aliases:\n%s", available.Body)
	}
	if got := strings.Join(available.SkillLines, "\n"); !strings.Contains(got, root+"/alpha/SKILL.md") || strings.Contains(got, "r0/") {
		t.Fatalf("SkillLines = %#v", available.SkillLines)
	}
}

func TestRenderAvailableSkillsCanOmitUsageInstructionsLikeRust(t *testing.T) {
	skills := []InstructionsSkillMetadata{
		{Name: "alpha", Scope: "repo", Description: "Alpha", Path: "/tmp/alpha/SKILL.md"},
	}
	available := RenderAvailableSkillsWithOptions(skills, AvailableSkillsRenderOptions{
		Budget:                   SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1000},
		IncludeUsageInstructions: false,
	})
	if available == nil {
		t.Fatalf("RenderAvailableSkillsWithOptions() = nil")
	}
	if strings.Contains(available.Body, "### How to use skills") || strings.Contains(available.Body, "read its `SKILL.md` completely") {
		t.Fatalf("Body included usage instructions:\n%s", available.Body)
	}
	if !strings.Contains(available.Body, "### Available skills") || !strings.Contains(available.Body, "alpha") {
		t.Fatalf("Body missing skills catalog:\n%s", available.Body)
	}
}

func TestRenderAvailableSkillsUsesAliasesWhenTheyAllowMoreSkillsToFitLikeRust(t *testing.T) {
	root := "/Users/xl/.codex/plugins/cache/openai-curated/example/hash1234567890/skills-with-a-very-long-shared-prefix"
	skills := make([]InstructionsSkillMetadata, 0, 12)
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("shared-root-skill-%d", i)
		skills = append(skills, InstructionsSkillMetadata{
			Name:  name,
			Scope: "repo",
			Path:  fmt.Sprintf("%s/skill-%d/SKILL.md", root, i),
			Root:  root,
		})
	}

	lines := orderedSkillRenderLines(skills)
	plan, ok := buildSkillAliasPlan(lines, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20})
	if !ok {
		t.Fatalf("buildSkillAliasPlan() did not build")
	}
	absoluteMinimum := 0
	for _, line := range lines {
		absoluteMinimum += SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20}.cost(line.renderMinimum() + "\n")
	}
	aliasMinimum := plan.tableCost
	for _, line := range applySkillAliases(lines, plan) {
		aliasMinimum += SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20}.cost(line.renderMinimum() + "\n")
	}
	if aliasMinimum >= absoluteMinimum {
		t.Fatalf("test fixture should make aliases cheaper: alias=%d absolute=%d", aliasMinimum, absoluteMinimum)
	}

	available := RenderAvailableSkills(skills, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: aliasMinimum})
	if available == nil {
		t.Fatalf("RenderAvailableSkills() = nil")
	}
	if available.Report.IncludedCount != len(skills) || available.Report.OmittedCount != 0 {
		t.Fatalf("Report = %#v", available.Report)
	}
	wantRootLine := fmt.Sprintf("- `r0` = `%s`", root)
	if len(available.SkillRootLines) != 1 || available.SkillRootLines[0] != wantRootLine {
		t.Fatalf("SkillRootLines = %#v, want %#v", available.SkillRootLines, []string{wantRootLine})
	}
	renderedText := strings.Join(available.SkillLines, "\n")
	if !strings.Contains(renderedText, "r0/skill-0/SKILL.md") || !strings.Contains(renderedText, "r0/skill-11/SKILL.md") {
		t.Fatalf("SkillLines = %#v", available.SkillLines)
	}
	if !strings.Contains(available.Body, "### Skill roots") || !strings.Contains(available.Body, "expand the listed short `path`") {
		t.Fatalf("Body missing alias instructions:\n%s", available.Body)
	}
}

func TestBuildSkillAliasPlanUsesMarketplaceRootForSingleSkillPluginVersionLikeRust(t *testing.T) {
	githubRoot := "/Users/xl/.codex/plugins/cache/openai-curated/github/hash123/skills"
	marketplaceRoot := "/Users/xl/.codex/plugins/cache/openai-curated"
	skills := []InstructionsSkillMetadata{
		{Name: "github:gh-fix-ci", Scope: "repo", Path: githubRoot + "/gh-fix-ci/SKILL.md", Root: githubRoot},
	}

	lines := orderedSkillRenderLines(skills)
	plan, ok := buildSkillAliasPlan(lines, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20})
	if !ok {
		t.Fatalf("buildSkillAliasPlan() did not build")
	}
	wantRootLines := []string{fmt.Sprintf("- `r0` = `%s`", marketplaceRoot)}
	if strings.Join(plan.rootLines, "\n") != strings.Join(wantRootLines, "\n") {
		t.Fatalf("rootLines = %#v, want %#v", plan.rootLines, wantRootLines)
	}
	got := applySkillAliases(lines, plan)[0].path
	want := "r0/github/hash123/skills/gh-fix-ci/SKILL.md"
	if got != want {
		t.Fatalf("aliased path = %q, want %q", got, want)
	}
}

func TestBuildSkillAliasPlanUsesSkillRootForMultipleSkillsInOnePluginVersionLikeRust(t *testing.T) {
	githubRoot := "/Users/xl/.codex/plugins/cache/openai-curated/github/hash123/skills"
	skills := []InstructionsSkillMetadata{
		{Name: "github:gh-fix-ci", Scope: "repo", Path: githubRoot + "/gh-fix-ci/SKILL.md", Root: githubRoot},
		{Name: "github:yeet", Scope: "repo", Path: githubRoot + "/yeet/SKILL.md", Root: githubRoot},
	}

	lines := orderedSkillRenderLines(skills)
	plan, ok := buildSkillAliasPlan(lines, SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 1 << 20})
	if !ok {
		t.Fatalf("buildSkillAliasPlan() did not build")
	}
	wantRootLines := []string{fmt.Sprintf("- `r0` = `%s`", githubRoot)}
	if strings.Join(plan.rootLines, "\n") != strings.Join(wantRootLines, "\n") {
		t.Fatalf("rootLines = %#v, want %#v", plan.rootLines, wantRootLines)
	}
	aliased := applySkillAliases(lines, plan)
	if aliased[0].path != "r0/gh-fix-ci/SKILL.md" || aliased[1].path != "r0/yeet/SKILL.md" {
		t.Fatalf("aliased paths = %#v", []string{aliased[0].path, aliased[1].path})
	}
}

func TestDefaultSkillMetadataBudget(t *testing.T) {
	if got := DefaultSkillMetadataBudget(200000); got.Kind != SkillMetadataBudgetTokens || got.Limit != 4000 {
		t.Fatalf("DefaultSkillMetadataBudget(200000) = %#v", got)
	}
	if got := DefaultSkillMetadataBudget(1000000); got.Kind != SkillMetadataBudgetTokens || got.Limit != 20000 {
		t.Fatalf("DefaultSkillMetadataBudget(1000000) = %#v", got)
	}
	if got := DefaultSkillMetadataBudget(0); got.Kind != SkillMetadataBudgetCharacters || got.Limit != DefaultSkillMetadataCharBudget {
		t.Fatalf("DefaultSkillMetadataBudget(0) = %#v", got)
	}
}

func TestRenderCombinedAvailableSkillsSharesBudgetAndPrioritizesExecutor(t *testing.T) {
	host := []InstructionsSkillMetadata{
		{Name: "host-one", Description: strings.Repeat("h", 100), Path: "C:/skills/host-one/SKILL.md", Root: "C:/skills", Scope: "user"},
		{Name: "host-two", Description: strings.Repeat("h", 100), Path: "C:/skills/host-two/SKILL.md", Root: "C:/skills", Scope: "user"},
	}
	executor := []InstructionsSkillMetadata{
		{Name: "executor-one", Description: strings.Repeat("e", 100), Path: "skill://executor/one", LocatorKind: "executor package", PackageID: "skill://executor/one"},
		{Name: "executor-two", Description: strings.Repeat("e", 100), Path: "skill://executor/two", LocatorKind: "executor package", PackageID: "skill://executor/two"},
	}
	options := AvailableSkillsRenderOptions{Budget: SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 155}}
	hostAvailable, executorAvailable := RenderCombinedAvailableSkills(host, executor, options)
	if hostAvailable == nil || executorAvailable == nil {
		t.Fatalf("combined render = host %#v executor %#v", hostAvailable, executorAvailable)
	}
	if executorAvailable.Report.IncludedCount != len(executor) {
		t.Fatalf("executor report = %#v", executorAvailable.Report)
	}
	if hostAvailable.Report.OmittedCount == 0 {
		t.Fatalf("host report did not absorb shared budget pressure: %#v", hostAvailable.Report)
	}
	if got := linesCost(options.Budget, append(append([]string(nil), executorAvailable.SkillLines...), hostAvailable.SkillLines...)) + aliasedMetadataOverheadCost(options.Budget, hostAvailable.SkillRootLines); got > options.Budget.Limit {
		t.Fatalf("combined metadata cost = %d, budget = %d", got, options.Budget.Limit)
	}
}

func TestRenderCombinedAvailableSkillsReservesExecutorOmissionMarker(t *testing.T) {
	executor := make([]InstructionsSkillMetadata, 0, 4)
	for index := 0; index < 4; index++ {
		executor = append(executor, InstructionsSkillMetadata{Name: fmt.Sprintf("executor-%d", index), Path: fmt.Sprintf("skill://executor/%d", index), LocatorKind: "executor package", PackageID: fmt.Sprintf("skill://executor/%d", index)})
	}
	host := []InstructionsSkillMetadata{{Name: "host", Path: "C:/skills/host/SKILL.md", Root: "C:/skills"}}
	_, executorAvailable := RenderCombinedAvailableSkills(host, executor, AvailableSkillsRenderOptions{Budget: SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: 120}})
	if executorAvailable == nil || executorAvailable.Report.OmittedCount == 0 {
		t.Fatalf("executor report = %#v", executorAvailable)
	}
	if !strings.Contains(strings.Join(executorAvailable.SkillLines, "\n"), "additional skill") {
		t.Fatalf("executor omission marker missing: %#v", executorAvailable.SkillLines)
	}
}

func TestRenderExtensionAvailableSkillsMatchesRustBoundedCatalog(t *testing.T) {
	longDescription := strings.Repeat("x", 1025)
	skills := []InstructionsSkillMetadata{
		{Name: "second", Description: "Second", Path: "skill://root/second/SKILL.md", LocatorKind: "executor package", PackageID: "skill://root/second"},
		{Name: "first", Description: longDescription, Path: "skill://root/first/SKILL.md", LocatorKind: "executor package", PackageID: "skill://root/first"},
	}
	available := RenderExtensionAvailableSkills(skills, false)
	if available == nil || len(available.SkillLines) != 2 {
		t.Fatalf("extension available skills = %#v", available)
	}
	if !strings.HasPrefix(available.SkillLines[0], "- second: Second ") {
		t.Fatalf("extension catalog order changed: %#v", available.SkillLines)
	}
	if !strings.Contains(available.SkillLines[0], "(executor package: skill://root/second)") {
		t.Fatalf("extension catalog locator = %q", available.SkillLines[0])
	}
	wantTruncated := strings.Repeat("x", 1021) + "..."
	if !strings.Contains(available.SkillLines[1], wantTruncated) || strings.Contains(available.SkillLines[1], longDescription) {
		t.Fatalf("extension description truncation = %q", available.SkillLines[1])
	}
}

func TestRenderExtensionAvailableSkillsPreservesEntriesBeforeOmittingLikeRust(t *testing.T) {
	skills := make([]InstructionsSkillMetadata, 0, 10)
	for index := 0; index < 10; index++ {
		skills = append(skills, InstructionsSkillMetadata{
			Name:        fmt.Sprintf("skill-%02d", index),
			Description: strings.Repeat("x", 1024),
			Path:        fmt.Sprintf("skill://root/skill-%02d/SKILL.md", index),
			LocatorKind: "executor package",
			PackageID:   fmt.Sprintf("skill://root/skill-%02d", index),
		})
	}
	available := RenderExtensionAvailableSkills(skills, false)
	if available == nil || available.Report == nil || available.Report.OmittedCount != 0 || available.Report.IncludedCount != len(skills) {
		t.Fatalf("extension pressure report = %#v", available)
	}
	if available.WarningMessage == nil || strings.Contains(available.Body, "additional skills omitted") || available.Report.TruncatedDescriptionSkillCount != len(skills) {
		t.Fatalf("extension bounded catalog body = %q warning=%v", available.Body, available.WarningMessage)
	}
	if strings.Contains(strings.Join(available.SkillLines, "\n"), "/SKILL.md") {
		t.Fatalf("extension catalog still renders full SKILL.md locators: %#v", available.SkillLines)
	}
}
