package prompt

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const (
	DefaultSkillMetadataCharBudget       = 8000
	SkillMetadataContextWindowPercent    = 2
	MaxDefaultContextSkillDescriptionLen = 1024
	SkillDescriptionTruncatedSuffix      = "..."
	SkillDescriptionWarningThreshold     = 100

	SkillDescriptionTruncatedWarning            = "Skill descriptions were shortened to fit the skills context budget. Codex can still see every skill, but some descriptions are shorter. Disable unused skills or plugins to leave more room for the rest."
	SkillDescriptionTruncatedWarningWithPercent = "Skill descriptions were shortened to fit the 2% skills context budget. Codex can still see every skill, but some descriptions are shorter. Disable unused skills or plugins to leave more room for the rest."
	SkillsInstructionsOpenTag                   = "<skills_instructions>"
	SkillsInstructionsCloseTag                  = "</skills_instructions>"
	SkillsIntroWithAbsolutePaths                = "A skill is a set of instructions provided through a `SKILL.md` source. Below is the list of skills that can be used. Each entry includes a name, description, and source locator. `file` locators are on the host filesystem, `environment resource` locators are owned by an execution environment, `orchestrator resource` locators are opaque non-filesystem resources, and `custom resource` locators use their provider's access mechanism."
	SkillsIntroWithAliases                      = "A skill is a set of local instructions to follow that is stored in a `SKILL.md` file. Below is the list of skills that can be used. Each entry includes a name, description, and a short path that can be expanded into an absolute path using the skill roots table."
)

var SkillsHowToUseWithAbsolutePaths = strings.Join([]string{
	"- Discovery: The list above is the skills available in this session (name + description + source locator). `file` entries live on the host filesystem, `environment resource` entries are owned by their execution environment, `orchestrator resource` entries must be accessed through `skills.list` and `skills.read`, and `custom resource` entries use their provider's access mechanism.",
	"- Trigger rules: If the user names a skill (with `$SkillName` or plain text) OR the task clearly matches a skill's description shown above, you must use that skill for that turn. Multiple mentions mean use them all. Do not carry skills across turns unless re-mentioned.",
	"- Missing/blocked: If a named skill isn't in the list or its source can't be read, say so briefly and continue with the best fallback.",
	"- How to use a skill (progressive disclosure):",
	"  1) After deciding to use a skill, the main agent must read its `SKILL.md` completely before taking task actions. For a `file` entry, open the listed path. For an `environment resource`, use the filesystem of the owning environment. For an `orchestrator resource`, call `skills.list` with `{\"authority\":{\"kind\":\"orchestrator\"}}`, select the matching package, and pass its `main_resource` to `skills.read`. If a read is truncated or paginated, continue until EOF.",
	"  2) When `SKILL.md` references another resource, use the same access mechanism. Resolve relative paths against a filesystem-backed skill directory. For orchestrator skills, pass the exact referenced resource identifier with the same authority and package to `skills.read`; do not treat `skill://` identifiers as filesystem paths.",
	"  3) If `SKILL.md` points to extra folders such as `references/`, use its routing instructions to identify the resources required for the task. The main agent must read each required instruction or reference file itself before acting on it. Do not delegate reading, summarizing, or interpreting skill instructions to a subagent. Subagents may still perform task work when the selected skill allows it.",
	"  4) For filesystem-backed skills, prefer running or patching provided scripts instead of retyping large code blocks. For orchestrator skills, use `skills.read` and the available tools; do not invent a local path.",
	"  5) Reuse provided assets or templates through the same source access mechanism instead of recreating them.",
	"- Coordination and sequencing:",
	"  - If multiple skills apply, choose the minimal set that covers the request and state the order you'll use them.",
	"  - Announce which skill(s) you're using and why (one short line). If you skip an obvious skill, say why.",
	"- Context hygiene:",
	"  - Progressive disclosure applies to selecting relevant files, not partially reading a selected instruction file. Do not load unrelated references, scripts, or assets.",
	"  - Avoid deep reference-chasing: prefer opening only files directly linked from `SKILL.md` unless you're blocked.",
	"  - When variants exist (frameworks, providers, domains), pick only the relevant reference file(s) and note that choice.",
	"- Safety and fallback: If a skill can't be applied cleanly (missing files, unclear instructions), state the issue, pick the next-best approach, and continue.",
}, "\n")

var SkillsHowToUseWithAliases = strings.Join([]string{
	"- Discovery: The list above is the skills available in this session (name + description + short path). Skill bodies live on disk at the listed paths after expanding the matching alias from `### Skill roots`.",
	"- Trigger rules: If the user names a skill (with `$SkillName` or plain text) OR the task clearly matches a skill's description shown above, you must use that skill for that turn. Multiple mentions mean use them all. Do not carry skills across turns unless re-mentioned.",
	"- Missing/blocked: If a named skill isn't in the list or the path can't be read, say so briefly and continue with the best fallback.",
	"- How to use a skill (progressive disclosure):",
	"  1) After deciding to use a skill, the main agent must expand the listed short `path` with the matching alias from `### Skill roots`, then open and read its `SKILL.md` completely before taking task actions. If a read is truncated or paginated, continue until EOF.",
	"  2) When `SKILL.md` references relative paths (e.g., `scripts/foo.py`), resolve them relative to the directory containing that expanded `SKILL.md` first, and only consider other paths if needed.",
	"  3) If `SKILL.md` points to extra folders such as `references/`, use its routing instructions to identify the files required for the task. The main agent must read each required instruction or reference file itself before acting on it. Do not delegate reading, summarizing, or interpreting skill instructions to a subagent. Subagents may still perform task work when the selected skill allows it.",
	"  4) If `scripts/` exist, prefer running or patching them instead of retyping large code blocks.",
	"  5) If `assets/` or templates exist, reuse them instead of recreating from scratch.",
	"- Coordination and sequencing:",
	"  - If multiple skills apply, choose the minimal set that covers the request and state the order you'll use them.",
	"  - Announce which skill(s) you're using and why (one short line). If you skip an obvious skill, say why.",
	"- Context hygiene:",
	"  - Progressive disclosure applies to selecting relevant files, not partially reading a selected instruction file. Do not load unrelated references, scripts, or assets.",
	"  - Avoid deep reference-chasing: prefer opening only files directly linked from `SKILL.md` unless you're blocked.",
	"  - When variants exist (frameworks, providers, domains), pick only the relevant reference file(s) and note that choice.",
	"- Safety and fallback: If a skill can't be applied cleanly (missing files, unclear instructions), state the issue, pick the next-best approach, and continue.",
}, "\n")

type SkillMetadataBudgetKind string

const (
	SkillMetadataBudgetCharacters SkillMetadataBudgetKind = "characters"
	SkillMetadataBudgetTokens     SkillMetadataBudgetKind = "tokens"
)

type SkillMetadataBudget struct {
	Kind  SkillMetadataBudgetKind
	Limit int
}

type AvailableSkillsRenderOptions struct {
	Budget                   SkillMetadataBudget
	IncludeUsageInstructions bool
}

type AvailableSkills struct {
	Body           string
	SkillRootLines []string
	SkillLines     []string
	Report         *SkillRenderReport
	WarningMessage *string
}

type SkillRenderReport struct {
	TotalCount                     int
	IncludedCount                  int
	OmittedCount                   int
	TruncatedDescriptionChars      int
	TruncatedDescriptionSkillCount int
}

type skillRenderLine struct {
	name        string
	description string
	path        string
	locatorKind string
	root        string
	scope       string
}

type skillAliasPlan struct {
	rootLines       []string
	rootAliases     map[string]string
	aliasRootByPath map[string]string
	tableCost       int
}

func DefaultSkillMetadataBudget(contextWindow int64) SkillMetadataBudget {
	if contextWindow > 0 {
		limit := int(contextWindow) * SkillMetadataContextWindowPercent / 100
		if limit < 1 {
			limit = 1
		}
		if limit > 4000 {
			limit = 4000
		}
		return SkillMetadataBudget{Kind: SkillMetadataBudgetTokens, Limit: limit}
	}
	return SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget}
}

func RenderAvailableSkills(skills []InstructionsSkillMetadata, budget SkillMetadataBudget) *AvailableSkills {
	return RenderAvailableSkillsWithOptions(skills, AvailableSkillsRenderOptions{Budget: budget, IncludeUsageInstructions: true})
}

func RenderAvailableSkillsWithOptions(skills []InstructionsSkillMetadata, options AvailableSkillsRenderOptions) *AvailableSkills {
	lines := orderedSkillRenderLines(skills)
	if len(lines) == 0 {
		return nil
	}
	budget := options.Budget
	if budget.Limit <= 0 {
		budget = SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget}
	}
	skillLines, report, skillRootLines := renderBestSkillLines(lines, budget)
	warning := skillRenderWarning(report, budget)
	return &AvailableSkills{
		Body:           renderAvailableSkillsBody(skillRootLines, skillLines, options.IncludeUsageInstructions),
		SkillRootLines: skillRootLines,
		SkillLines:     skillLines,
		Report:         report,
		WarningMessage: warning,
	}
}

func RenderExtensionAvailableSkills(skills []InstructionsSkillMetadata, includeUsageInstructions bool) *AvailableSkills {
	skillLines := make([]string, 0, len(skills))
	totalBytes := 0
	omitted := 0
	eligible := 0
	for _, skill := range skills {
		if !skill.AllowsImplicitInvocation() {
			continue
		}
		eligible++
		description := truncateSkillDescription(skill.Description)
		path := strings.ReplaceAll(firstNonEmptyString(skill.LocatorPath, skill.Path), "\\", "/")
		locatorKind := strings.TrimSpace(skill.LocatorKind)
		if locatorKind == "" {
			locatorKind = "file"
		}
		line := "- " + skill.Name + ": "
		if description != "" {
			line += description + " "
		}
		line += "(" + locatorKind + ": " + path + ")"
		if totalBytes+len(line) > DefaultSkillMetadataCharBudget {
			omitted++
			continue
		}
		totalBytes += len(line)
		skillLines = append(skillLines, line)
	}
	if len(skillLines) == 0 {
		return nil
	}
	if omitted > 0 {
		word := "skills"
		if omitted == 1 {
			word = "skill"
		}
		skillLines = append(skillLines, fmt.Sprintf("- %d additional %s omitted from this bounded skills list.", omitted, word))
	}
	return &AvailableSkills{
		Body:       renderAvailableSkillsBody(nil, skillLines, includeUsageInstructions),
		SkillLines: skillLines,
		Report: &SkillRenderReport{
			TotalCount:    eligible,
			IncludedCount: eligible - omitted,
			OmittedCount:  omitted,
		},
	}
}

func orderedSkillRenderLines(skills []InstructionsSkillMetadata) []skillRenderLine {
	lines := make([]skillRenderLine, 0, len(skills))
	for _, skill := range skills {
		if !skill.AllowsImplicitInvocation() {
			continue
		}
		path := firstNonEmptyString(skill.LocatorPath, skill.Path)
		locatorKind := strings.TrimSpace(skill.LocatorKind)
		if locatorKind == "" {
			locatorKind = "file"
		}
		lines = append(lines, skillRenderLine{
			name:        skill.Name,
			description: truncateSkillDescription(skill.Description),
			path:        strings.ReplaceAll(path, "\\", "/"),
			locatorKind: locatorKind,
			root:        cleanSkillAliasRoot(skill.Root),
			scope:       skill.Scope,
		})
	}
	sort.SliceStable(lines, func(i int, j int) bool {
		if rank := skillScopeRank(lines[i].scope) - skillScopeRank(lines[j].scope); rank != 0 {
			return rank < 0
		}
		if lines[i].name == lines[j].name {
			return lines[i].path < lines[j].path
		}
		return lines[i].name < lines[j].name
	})
	return lines
}

func renderBestSkillLines(lines []skillRenderLine, budget SkillMetadataBudget) ([]string, *SkillRenderReport, []string) {
	absoluteLines, absoluteReport := renderSkillLines(lines, budget)
	if absoluteReport.OmittedCount == 0 && absoluteReport.TruncatedDescriptionChars == 0 {
		return absoluteLines, absoluteReport, nil
	}

	plan, ok := buildSkillAliasPlan(lines, budget)
	if !ok || plan.tableCost >= budget.Limit {
		return absoluteLines, absoluteReport, nil
	}
	adjustedBudget := budget
	adjustedBudget.Limit -= plan.tableCost
	aliasedLines, aliasedReport := renderSkillLines(applySkillAliases(lines, plan), adjustedBudget)
	if aliasedRenderIsBetter(aliasedLines, aliasedReport, plan.rootLines, absoluteLines, absoluteReport, nil, budget) {
		return aliasedLines, aliasedReport, plan.rootLines
	}
	return absoluteLines, absoluteReport, nil
}

func renderSkillLines(lines []skillRenderLine, budget SkillMetadataBudget) ([]string, *SkillRenderReport) {
	fullCost := 0
	for _, line := range lines {
		fullCost += budget.cost(line.renderFull() + "\n")
	}
	if fullCost <= budget.Limit {
		out := make([]string, 0, len(lines))
		for _, line := range lines {
			out = append(out, line.renderFull())
		}
		return out, &SkillRenderReport{TotalCount: len(lines), IncludedCount: len(lines)}
	}

	minimumCost := 0
	for _, line := range lines {
		minimumCost += budget.cost(line.renderMinimum() + "\n")
	}
	if minimumCost <= budget.Limit {
		allocations := allocateDescriptionBudget(lines, budget, budget.Limit-minimumCost)
		out := make([]string, 0, len(lines))
		truncatedChars := 0
		truncatedCount := 0
		for i, line := range lines {
			chars := allocations[i]
			out = append(out, line.renderWithDescriptionChars(chars))
			if chars < len([]rune(line.description)) {
				truncatedChars += len([]rune(line.description)) - chars
				if line.description != "" {
					truncatedCount++
				}
			}
		}
		return out, &SkillRenderReport{
			TotalCount:                     len(lines),
			IncludedCount:                  len(lines),
			TruncatedDescriptionChars:      truncatedChars,
			TruncatedDescriptionSkillCount: truncatedCount,
		}
	}

	out := make([]string, 0, len(lines))
	used := 0
	omitted := 0
	truncatedChars := 0
	truncatedCount := 0
	for _, line := range lines {
		cost := budget.cost(line.renderMinimum() + "\n")
		descriptionChars := len([]rune(line.description))
		if used+cost <= budget.Limit {
			used += cost
			out = append(out, line.renderMinimum())
		} else {
			omitted++
		}
		truncatedChars += descriptionChars
		if descriptionChars > 0 {
			truncatedCount++
		}
	}
	return out, &SkillRenderReport{
		TotalCount:                     len(lines),
		IncludedCount:                  len(out),
		OmittedCount:                   omitted,
		TruncatedDescriptionChars:      truncatedChars,
		TruncatedDescriptionSkillCount: truncatedCount,
	}
}

func buildSkillAliasPlan(lines []skillRenderLine, budget SkillMetadataBudget) (*skillAliasPlan, bool) {
	usedRoots := make([]string, 0, len(lines))
	for _, line := range lines {
		root := cleanSkillAliasRoot(line.root)
		if root == "" || !skillPathHasRoot(line.path, root) {
			continue
		}
		usedRoots = append(usedRoots, root)
	}
	if len(usedRoots) == 0 {
		return nil, false
	}

	pluginVersionCounts := pluginVersionSkillCountsForSkillRoots(usedRoots)
	aliasRootBySkillRoot := make(map[string]string, len(usedRoots))
	aliasRoots := make([]string, 0, len(usedRoots))
	seenAliasRoots := map[string]bool{}
	for _, root := range usedRoots {
		aliasRoot, ok := aliasRootForSkillRoot(root, pluginVersionCounts)
		if !ok {
			aliasRoot = root
		}
		aliasRootBySkillRoot[root] = aliasRoot
		if !seenAliasRoots[aliasRoot] {
			seenAliasRoots[aliasRoot] = true
			aliasRoots = append(aliasRoots, aliasRoot)
		}
	}
	if len(aliasRoots) == 0 {
		return nil, false
	}

	rootAliases := make(map[string]string, len(aliasRoots))
	rootLines := make([]string, 0, len(aliasRoots))
	for i, root := range aliasRoots {
		alias := fmt.Sprintf("r%d", i)
		rootAliases[root] = alias
		rootLines = append(rootLines, fmt.Sprintf("- `%s` = `%s`", alias, root))
	}

	aliasRootByPath := make(map[string]string, len(lines))
	for _, line := range lines {
		root := cleanSkillAliasRoot(line.root)
		aliasRoot := aliasRootBySkillRoot[root]
		if aliasRoot == "" || !skillPathHasRoot(line.path, aliasRoot) {
			continue
		}
		aliasRootByPath[line.path] = aliasRoot
	}
	if len(aliasRootByPath) == 0 {
		return nil, false
	}

	return &skillAliasPlan{
		rootLines:       rootLines,
		rootAliases:     rootAliases,
		aliasRootByPath: aliasRootByPath,
		tableCost:       aliasedMetadataOverheadCost(budget, rootLines),
	}, true
}

func applySkillAliases(lines []skillRenderLine, plan *skillAliasPlan) []skillRenderLine {
	if plan == nil {
		return lines
	}
	out := make([]skillRenderLine, 0, len(lines))
	for _, line := range lines {
		if aliasRoot := plan.aliasRootByPath[line.path]; aliasRoot != "" {
			if alias := plan.rootAliases[aliasRoot]; alias != "" {
				if relative, ok := skillPathRelativeToRoot(line.path, aliasRoot); ok {
					line.path = alias + "/" + relative
				}
			}
		}
		out = append(out, line)
	}
	return out
}

func aliasedRenderIsBetter(aliasedLines []string, aliasedReport *SkillRenderReport, aliasedRootLines []string, absoluteLines []string, absoluteReport *SkillRenderReport, absoluteRootLines []string, budget SkillMetadataBudget) bool {
	if aliasedReport.IncludedCount != absoluteReport.IncludedCount {
		return aliasedReport.IncludedCount > absoluteReport.IncludedCount
	}
	if aliasedReport.TruncatedDescriptionChars != absoluteReport.TruncatedDescriptionChars {
		return aliasedReport.TruncatedDescriptionChars < absoluteReport.TruncatedDescriptionChars
	}
	return availableSkillsCost(budget, aliasedRootLines, aliasedLines) < availableSkillsCost(budget, absoluteRootLines, absoluteLines)
}

func availableSkillsCost(budget SkillMetadataBudget, rootLines []string, skillLines []string) int {
	cost := linesCost(budget, skillLines)
	if len(rootLines) > 0 {
		cost += aliasedMetadataOverheadCost(budget, rootLines)
	}
	return cost
}

func linesCost(budget SkillMetadataBudget, lines []string) int {
	cost := 0
	for _, line := range lines {
		cost += budget.cost(line + "\n")
	}
	return cost
}

func aliasedMetadataOverheadCost(budget SkillMetadataBudget, skillRootLines []string) int {
	absoluteCost := budget.cost(renderAvailableSkillsBudgetBody(nil, nil))
	aliasedCost := budget.cost(renderAvailableSkillsBudgetBody(skillRootLines, nil))
	if aliasedCost < absoluteCost {
		return 0
	}
	return aliasedCost - absoluteCost
}

func renderAvailableSkillsBudgetBody(skillRootLines []string, skillLines []string) string {
	var lines []string
	lines = append(lines, "## Skills")
	if len(skillRootLines) == 0 {
		lines = append(lines, SkillsIntroWithAbsolutePaths)
	} else {
		lines = append(lines, SkillsIntroWithAliases)
		lines = append(lines, "### Skill roots")
		lines = append(lines, skillRootLines...)
	}
	lines = append(lines, "### Available skills")
	lines = append(lines, skillLines...)
	return "\n" + strings.Join(lines, "\n") + "\n"
}

func allocateDescriptionBudget(lines []skillRenderLine, budget SkillMetadataBudget, remaining int) []int {
	allocations := make([]int, len(lines))
	for remaining > 0 {
		progressed := false
		for i, line := range lines {
			descriptionChars := len([]rune(line.description))
			if allocations[i] >= descriptionChars {
				continue
			}
			current := line.renderWithDescriptionChars(allocations[i])
			next := line.renderWithDescriptionChars(allocations[i] + 1)
			addedCost := budget.cost(next+"\n") - budget.cost(current+"\n")
			if addedCost <= 0 {
				addedCost = 1
			}
			if addedCost > remaining {
				continue
			}
			allocations[i]++
			remaining -= addedCost
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return allocations
}

func renderAvailableSkillsBody(skillRootLines []string, skillLines []string, includeUsageInstructions bool) string {
	intro := SkillsIntroWithAbsolutePaths
	howToUse := SkillsHowToUseWithAbsolutePaths
	if len(skillRootLines) > 0 {
		intro = SkillsIntroWithAliases
		howToUse = SkillsHowToUseWithAliases
	}

	var builder strings.Builder
	builder.WriteString("\n")
	builder.WriteString(SkillsInstructionsOpenTag)
	builder.WriteString("\n")
	builder.WriteString("\n## Skills\n")
	builder.WriteString(intro)
	builder.WriteString("\n")
	if len(skillRootLines) > 0 {
		builder.WriteString("### Skill roots\n")
		builder.WriteString(strings.Join(skillRootLines, "\n"))
		builder.WriteString("\n")
	}
	builder.WriteString("### Available skills\n")
	builder.WriteString(strings.Join(skillLines, "\n"))
	if includeUsageInstructions {
		builder.WriteString("\n### How to use skills\n")
		builder.WriteString(howToUse)
	}
	builder.WriteString("\n")
	builder.WriteString(SkillsInstructionsCloseTag)
	builder.WriteString("\n")
	return builder.String()
}

func cleanSkillAliasRoot(root string) string {
	root = strings.TrimSpace(strings.ReplaceAll(root, "\\", "/"))
	if root == "" || strings.Contains(root, "://") {
		return ""
	}
	root = strings.ReplaceAll(filepath.Clean(root), "\\", "/")
	for strings.HasSuffix(root, "/") && root != "/" && !strings.HasSuffix(root, ":/") {
		root = strings.TrimSuffix(root, "/")
	}
	return root
}

func cleanSkillAliasPath(path string) string {
	path = strings.TrimSpace(strings.ReplaceAll(path, "\\", "/"))
	if path == "" || strings.Contains(path, "://") {
		return path
	}
	return strings.ReplaceAll(filepath.Clean(path), "\\", "/")
}

func skillPathHasRoot(path string, root string) bool {
	_, ok := skillPathRelativeToRoot(path, root)
	return ok
}

func skillPathRelativeToRoot(path string, root string) (string, bool) {
	root = cleanSkillAliasRoot(root)
	path = cleanSkillAliasPath(path)
	if root == "" || path == "" || strings.Contains(path, "://") {
		return "", false
	}
	if path == root {
		return "", true
	}
	prefix := root
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	relative, ok := strings.CutPrefix(path, prefix)
	if !ok || relative == "" || strings.HasPrefix(relative, "../") {
		return "", false
	}
	return relative, true
}

func pluginVersionSkillCountsForSkillRoots(roots []string) map[string]int {
	counts := map[string]int{}
	for _, root := range roots {
		if versionBase, ok := pluginVersionBase(root); ok {
			counts[versionBase]++
		}
	}
	return counts
}

func aliasRootForSkillRoot(root string, pluginVersionCounts map[string]int) (string, bool) {
	versionBase, ok := pluginVersionBase(root)
	if !ok {
		return root, true
	}
	if pluginVersionCounts[versionBase] > 1 {
		return root, true
	}
	if marketplaceBase, ok := pluginMarketplaceBase(root); ok {
		return marketplaceBase, true
	}
	return root, true
}

func pluginMarketplaceBase(path string) (string, bool) {
	path = cleanSkillAliasRoot(path)
	if path == "" {
		return "", false
	}
	parts := strings.Split(path, "/")
	for i := len(parts) - 1; i >= 2; i-- {
		if parts[i-1] == "cache" && parts[i-2] == "plugins" {
			return strings.Join(parts[:i+1], "/"), true
		}
	}
	return "", false
}

func pluginVersionBase(path string) (string, bool) {
	marketplaceBase, ok := pluginMarketplaceBase(path)
	if !ok {
		return "", false
	}
	relative, ok := skillPathRelativeToRoot(path, marketplaceBase)
	if !ok {
		return "", false
	}
	parts := strings.Split(relative, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false
	}
	return marketplaceBase + "/" + parts[0] + "/" + parts[1], true
}

func skillRenderWarning(report *SkillRenderReport, budget SkillMetadataBudget) *string {
	if report == nil {
		return nil
	}
	if report.OmittedCount > 0 {
		word := "skills"
		verb := "were"
		if report.OmittedCount == 1 {
			word = "skill"
			verb = "was"
		}
		prefix := "Exceeded skills context budget."
		if budget.Kind == SkillMetadataBudgetTokens {
			prefix = fmt.Sprintf("Exceeded skills context budget of %d%%.", SkillMetadataContextWindowPercent)
		}
		message := fmt.Sprintf("%s All skill descriptions were removed and %d additional %s %s not included in the model-visible skills list.", prefix, report.OmittedCount, word, verb)
		return &message
	}
	if report.TotalCount > 0 && (report.TruncatedDescriptionChars+report.TotalCount-1)/report.TotalCount > SkillDescriptionWarningThreshold {
		message := SkillDescriptionTruncatedWarning
		if budget.Kind == SkillMetadataBudgetTokens {
			message = SkillDescriptionTruncatedWarningWithPercent
		}
		return &message
	}
	return nil
}

func (b SkillMetadataBudget) cost(value string) int {
	if b.Kind == SkillMetadataBudgetTokens {
		bytes := len(value)
		return (bytes + 3) / 4
	}
	return len([]rune(value))
}

func (l skillRenderLine) renderFull() string {
	return l.renderWithDescription(l.description)
}

func (l skillRenderLine) renderMinimum() string {
	return l.renderWithDescription("")
}

func (l skillRenderLine) renderWithDescriptionChars(chars int) string {
	if chars <= 0 {
		return l.renderMinimum()
	}
	descriptionRunes := []rune(l.description)
	if chars > len(descriptionRunes) {
		chars = len(descriptionRunes)
	}
	return l.renderWithDescription(string(descriptionRunes[:chars]))
}

func (l skillRenderLine) renderWithDescription(description string) string {
	path := l.path
	if path == "" {
		path = filepath.ToSlash(filepath.Join("skills", l.name, SkillDescriptionTruncatedSuffix))
	}
	locatorKind := strings.TrimSpace(l.locatorKind)
	if locatorKind == "" {
		locatorKind = "file"
	}
	if description == "" {
		return fmt.Sprintf("- %s: (%s: %s)", l.name, locatorKind, path)
	}
	return fmt.Sprintf("- %s: %s (%s: %s)", l.name, description, locatorKind, path)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateSkillDescription(description string) string {
	runes := []rune(description)
	if len(runes) <= MaxDefaultContextSkillDescriptionLen {
		return description
	}
	suffixLen := len([]rune(SkillDescriptionTruncatedSuffix))
	prefixLen := MaxDefaultContextSkillDescriptionLen - suffixLen
	if prefixLen < 0 {
		prefixLen = 0
	}
	return string(runes[:prefixLen]) + SkillDescriptionTruncatedSuffix
}

func skillScopeRank(scope string) int {
	switch strings.ToLower(scope) {
	case "system":
		return 0
	case "admin":
		return 1
	case "repo":
		return 2
	case "user":
		return 3
	default:
		return 4
	}
}
