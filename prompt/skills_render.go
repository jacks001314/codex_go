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
	SkillsIntroWithAbsolutePaths                = "A skill is a set of instructions provided through a `SKILL.md` source. Below is the list of skills that can be used. Each entry includes a name, description, and source locator. `file` locators are on the host filesystem, `executor package` locators are owned by their execution environment, `orchestrator package` locators are opaque package identifiers, and `custom resource` locators use their provider's access mechanism."
	SkillsIntroWithAliases                      = "A skill is a set of local instructions to follow that is stored in a `SKILL.md` file. Below is the list of skills that can be used. Each entry includes a name, description, and a short path that can be expanded into an absolute path using the skill roots table."
)

var SkillsHowToUseWithAbsolutePaths = strings.Join([]string{
	"- Discovery: The list above is the skills available in this session (name + description + source locator). `file` entries live on the host filesystem, `executor package` and `orchestrator package` entries are accessed directly through `skills.read`, and `custom resource` entries use their provider's access mechanism.",
	"- Trigger rules: If the user names a skill (with `$SkillName` or plain text) OR the task clearly matches a skill's description shown above, you must use that skill for that turn. Multiple mentions mean use them all. Do not carry skills across turns unless re-mentioned.",
	"- Missing/blocked: If a named skill isn't in the list or its source can't be read, say so briefly and continue with the best fallback.",
	"- How to use a skill (progressive disclosure):",
	"  1) After deciding to use a skill, the main agent must read its `SKILL.md` completely before taking task actions. For a `file` entry, open the listed path. For an `executor package` or `orchestrator package`, pass the listed locator directly to `skills.read` as `package`; root aliases are resolved automatically. Omit `resource` to read `SKILL.md` directly without calling `skills.list`. If a read is paginated, follow `next_cursor` until EOF.",
	"  2) When `SKILL.md` references another resource, use the same access mechanism. For executor and orchestrator skills, pass the complete package-contained resource identifier with the same package to `skills.read`; do not treat `skill://` identifiers as filesystem paths.",
	"  3) If `SKILL.md` points to extra folders such as `references/`, use its routing instructions to identify the resources required for the task. The main agent must read each required instruction or reference file itself before acting on it. Do not delegate reading, summarizing, or interpreting skill instructions to a subagent. Subagents may still perform task work when the selected skill allows it.",
	"  4) For filesystem-backed skills, prefer running or patching provided scripts instead of retyping large code blocks. For executor and orchestrator skills, use `skills.read` and the available tools; do not invent a local path.",
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
	// Configured marks a budget sourced from [skills].max_context_tokens
	// (Rust #38978). Configured budgets render the plain Rust warnings instead
	// of the context-window-percent variant.
	Configured bool
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
	name         string
	description  string
	path         string
	locatorKind  string
	root         string
	scope        string
	rootOrder    int
	hasRootOrder bool
}

type skillAliasPlan struct {
	rootLines       []string
	rootAliases     map[string]string
	aliasRootByPath map[string]string
	tableCost       int
}

type skillLineAllocation struct {
	omitted          bool
	descriptionChars int
}

type combinedAvailableSkillsRender struct {
	hostSkillLines     []string
	hostSkillRootLines []string
	hostReport         *SkillRenderReport
	executorSkillLines []string
	executorReport     *SkillRenderReport
}

func DefaultSkillMetadataBudget(contextWindow int64) SkillMetadataBudget {
	if contextWindow > 0 {
		limit := int(contextWindow) * SkillMetadataContextWindowPercent / 100
		if limit < 1 {
			limit = 1
		}
		return SkillMetadataBudget{Kind: SkillMetadataBudgetTokens, Limit: limit}
	}
	return SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget}
}

// MaxConfiguredSkillMetadataTokenBudget caps [skills].max_context_tokens at
// 10,000 tokens (Rust MAX_CONFIGURED_SKILL_METADATA_TOKEN_BUDGET, #38978).
const MaxConfiguredSkillMetadataTokenBudget = 10_000

// ConfiguredSkillMetadataBudget returns the token budget for the available-
// skills catalog from an explicit [skills].max_context_tokens value, capped at
// MaxConfiguredSkillMetadataTokenBudget. A non-positive value yields the zero
// budget so callers fall back to the context-window default (mirrors Rust
// NonZeroUsize).
func ConfiguredSkillMetadataBudget(maxTokens int) SkillMetadataBudget {
	if maxTokens <= 0 {
		return SkillMetadataBudget{}
	}
	limit := maxTokens
	if limit > MaxConfiguredSkillMetadataTokenBudget {
		limit = MaxConfiguredSkillMetadataTokenBudget
	}
	return SkillMetadataBudget{Kind: SkillMetadataBudgetTokens, Limit: limit, Configured: true}
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
	return RenderExtensionAvailableSkillsWithOptions(skills, AvailableSkillsRenderOptions{
		Budget:                   SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget},
		IncludeUsageInstructions: includeUsageInstructions,
	})
}

func RenderExtensionAvailableSkillsWithOptions(skills []InstructionsSkillMetadata, options AvailableSkillsRenderOptions) *AvailableSkills {
	lines := extensionSkillRenderLines(skills)
	if len(lines) == 0 {
		return nil
	}
	budget := normalizedSkillMetadataBudget(options.Budget)
	skillLines, report := renderSkillLines(lines, budget)
	skillLines = appendExtensionOmissionMarker(skillLines, report, budget)
	warning := skillRenderWarning(report, budget)
	return &AvailableSkills{
		Body:           renderAvailableSkillsBody(nil, skillLines, options.IncludeUsageInstructions),
		SkillLines:     skillLines,
		Report:         report,
		WarningMessage: warning,
	}
}

// RenderCombinedAvailableSkills shares one metadata budget between host and
// executor catalogs. Executor entries are allocated first to match the runtime
// extension surface, while host entries can still use path aliases under pressure.
func RenderCombinedAvailableSkills(hostSkills []InstructionsSkillMetadata, executorSkills []InstructionsSkillMetadata, options AvailableSkillsRenderOptions) (*AvailableSkills, *AvailableSkills) {
	hostLines := orderedSkillRenderLines(hostSkills)
	executorLines := extensionSkillRenderLines(executorSkills)
	budget := normalizedSkillMetadataBudget(options.Budget)
	if len(hostLines) == 0 || len(executorLines) == 0 {
		return RenderAvailableSkillsWithOptions(hostSkills, options), RenderExtensionAvailableSkillsWithOptions(executorSkills, options)
	}

	absolute := renderCombinedSkillLines(hostLines, executorLines, budget, nil)
	selected := absolute
	if !combinedSkillsFullyRendered(absolute) {
		if plan, ok := buildSkillAliasPlan(hostLines, budget); ok && plan.tableCost < budget.Limit {
			adjustedBudget := budget
			adjustedBudget.Limit -= plan.tableCost
			aliased := renderCombinedSkillLines(applySkillAliases(hostLines, plan), executorLines, adjustedBudget, plan.rootLines)
			if combinedSkillsRenderIsBetter(aliased, absolute, budget) {
				selected = aliased
			}
		}
		// Rust ce22ea9712: executor locators are compacted with provider-specific
		// `e` aliases alongside host `r` aliases under metadata pressure.
		if plan, ok := buildExtensionAliasPlan(executorLines, budget); ok && plan.tableCost < budget.Limit {
			adjustedBudget := budget
			adjustedBudget.Limit -= plan.tableCost
			aliased := renderCombinedSkillLines(applySkillAliases(hostLines, nil), applySkillAliases(executorLines, plan), adjustedBudget, nil)
			if combinedSkillsRenderIsBetter(aliased, selected, budget) {
				selected = aliased
			}
		}
	}

	hostWarning := skillRenderWarning(selected.hostReport, budget)
	executorWarning := skillRenderWarning(selected.executorReport, budget)
	return &AvailableSkills{
			Body:           renderAvailableSkillsBody(selected.hostSkillRootLines, selected.hostSkillLines, options.IncludeUsageInstructions),
			SkillRootLines: selected.hostSkillRootLines,
			SkillLines:     selected.hostSkillLines,
			Report:         selected.hostReport,
			WarningMessage: hostWarning,
		}, &AvailableSkills{
			Body:           renderAvailableSkillsBody(nil, selected.executorSkillLines, options.IncludeUsageInstructions),
			SkillLines:     selected.executorSkillLines,
			Report:         selected.executorReport,
			WarningMessage: executorWarning,
		}
}

func extensionSkillRenderLines(skills []InstructionsSkillMetadata) []skillRenderLine {
	lines := make([]skillRenderLine, 0, len(skills))
	for _, skill := range skills {
		if !skill.AllowsImplicitInvocation() {
			continue
		}
		locatorKind := strings.TrimSpace(skill.LocatorKind)
		if locatorKind == "" {
			locatorKind = "file"
		}
		lines = append(lines, skillRenderLine{name: skill.Name, description: truncateSkillDescription(skill.Description), path: skillRenderLocator(skill), locatorKind: locatorKind})
	}
	return lines
}

func isSkillPackageLocatorKind(locatorKind string) bool {
	switch strings.TrimSpace(locatorKind) {
	case "executor package", "orchestrator package":
		return true
	}
	return false
}

// skillRenderLocator resolves the model-visible locator for one catalog entry.
// Rust 69ae78291d (#38167): executor and orchestrator entries render their
// package id as the locator, and package ids may contain a literal backslash
// that must be preserved (no backslash normalization). Host and custom entries
// keep rendering their full path with forward-slash normalization.
func skillRenderLocator(skill InstructionsSkillMetadata) string {
	locatorKind := strings.TrimSpace(skill.LocatorKind)
	path := firstNonEmptyString(skill.LocatorPath, skill.Path)
	if isSkillPackageLocatorKind(locatorKind) {
		if pkg := strings.TrimSpace(skill.PackageID); pkg != "" {
			return pkg
		}
		return path
	}
	return strings.ReplaceAll(path, "\\", "/")
}

func appendExtensionOmissionMarker(skillLines []string, report *SkillRenderReport, budget SkillMetadataBudget) []string {
	if report.OmittedCount > 0 {
		marker := extensionOmissionMarker(report.OmittedCount)
		for len(skillLines) > 0 && linesCost(budget, append(skillLines, marker)) > budget.Limit {
			skillLines = skillLines[:len(skillLines)-1]
			report.IncludedCount--
			report.OmittedCount++
			marker = extensionOmissionMarker(report.OmittedCount)
		}
		skillLines = append(skillLines, marker)
	}
	return skillLines
}

func orderedSkillRenderLines(skills []InstructionsSkillMetadata) []skillRenderLine {
	lines := make([]skillRenderLine, 0, len(skills))
	for _, skill := range skills {
		if !skill.AllowsImplicitInvocation() {
			continue
		}
		locatorKind := strings.TrimSpace(skill.LocatorKind)
		if locatorKind == "" {
			locatorKind = "file"
		}
		lines = append(lines, skillRenderLine{
			name:         skill.Name,
			description:  truncateSkillDescription(skill.Description),
			path:         skillRenderLocator(skill),
			locatorKind:  locatorKind,
			root:         cleanSkillAliasRoot(skill.Root),
			scope:        skill.Scope,
			rootOrder:    skill.RootOrder,
			hasRootOrder: skill.HasRootOrder,
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
	return renderAllocatedSkillLines(lines, allocateSkillLines(lines, budget))
}

func allocateSkillLines(lines []skillRenderLine, budget SkillMetadataBudget) []skillLineAllocation {
	fullCost := 0
	for _, line := range lines {
		fullCost += budget.cost(line.renderFull() + "\n")
	}
	if fullCost <= budget.Limit {
		allocations := make([]skillLineAllocation, len(lines))
		for index, line := range lines {
			allocations[index].descriptionChars = len([]rune(line.description))
		}
		return allocations
	}

	minimumCost := 0
	for _, line := range lines {
		minimumCost += budget.cost(line.renderMinimum() + "\n")
	}
	if minimumCost <= budget.Limit {
		descriptionChars := allocateDescriptionBudget(lines, budget, budget.Limit-minimumCost)
		allocations := make([]skillLineAllocation, len(lines))
		for index := range allocations {
			allocations[index].descriptionChars = descriptionChars[index]
		}
		return allocations
	}

	allocations := make([]skillLineAllocation, len(lines))
	used := 0
	for index, line := range lines {
		cost := budget.cost(line.renderMinimum() + "\n")
		if used+cost <= budget.Limit {
			used += cost
		} else {
			allocations[index].omitted = true
		}
	}
	return allocations
}

func renderAllocatedSkillLines(lines []skillRenderLine, allocations []skillLineAllocation) ([]string, *SkillRenderReport) {
	out := make([]string, 0, len(lines))
	report := &SkillRenderReport{TotalCount: len(lines)}
	for index, line := range lines {
		allocation := allocations[index]
		descriptionChars := len([]rune(line.description))
		if allocation.omitted {
			report.OmittedCount++
			report.TruncatedDescriptionChars += descriptionChars
			if descriptionChars > 0 {
				report.TruncatedDescriptionSkillCount++
			}
			continue
		}
		report.IncludedCount++
		out = append(out, line.renderWithDescriptionChars(allocation.descriptionChars))
		if allocation.descriptionChars < descriptionChars {
			report.TruncatedDescriptionChars += descriptionChars - allocation.descriptionChars
			if descriptionChars > 0 {
				report.TruncatedDescriptionSkillCount++
			}
		}
	}
	return out, report
}

func renderCombinedSkillLines(hostLines []skillRenderLine, executorLines []skillRenderLine, budget SkillMetadataBudget, hostRootLines []string) *combinedAvailableSkillsRender {
	allLines := append(append([]skillRenderLine(nil), executorLines...), hostLines...)
	allocations := allocateSkillLines(allLines, budget)
	marker := reserveExecutorOmissionMarker(allLines, len(executorLines), budget, allocations)
	executorRendered, executorReport := renderAllocatedSkillLines(executorLines, allocations[:len(executorLines)])
	if marker != "" {
		executorRendered = append(executorRendered, marker)
	}
	hostRendered, hostReport := renderAllocatedSkillLines(hostLines, allocations[len(executorLines):])
	return &combinedAvailableSkillsRender{
		hostSkillLines:     hostRendered,
		hostSkillRootLines: hostRootLines,
		hostReport:         hostReport,
		executorSkillLines: executorRendered,
		executorReport:     executorReport,
	}
}

func reserveExecutorOmissionMarker(lines []skillRenderLine, executorCount int, budget SkillMetadataBudget, allocations []skillLineAllocation) string {
	for {
		omitted := 0
		for _, allocation := range allocations[:executorCount] {
			if allocation.omitted {
				omitted++
			}
		}
		if omitted == 0 {
			return ""
		}
		marker := extensionOmissionMarker(omitted)
		if allocatedSkillLinesCost(lines, allocations, budget)+budget.cost(marker+"\n") <= budget.Limit {
			return marker
		}
		removeIndex := -1
		for index := len(allocations) - 1; index >= executorCount; index-- {
			if !allocations[index].omitted {
				removeIndex = index
				break
			}
		}
		if removeIndex < 0 {
			for index := executorCount - 1; index >= 0; index-- {
				if !allocations[index].omitted {
					removeIndex = index
					break
				}
			}
		}
		if removeIndex < 0 {
			return ""
		}
		allocations[removeIndex].omitted = true
	}
}

func allocatedSkillLinesCost(lines []skillRenderLine, allocations []skillLineAllocation, budget SkillMetadataBudget) int {
	cost := 0
	for index, line := range lines {
		if allocations[index].omitted {
			continue
		}
		cost += budget.cost(line.renderWithDescriptionChars(allocations[index].descriptionChars) + "\n")
	}
	return cost
}

func combinedSkillsFullyRendered(rendered *combinedAvailableSkillsRender) bool {
	return rendered != nil && rendered.hostReport.OmittedCount == 0 && rendered.hostReport.TruncatedDescriptionChars == 0 && rendered.executorReport.OmittedCount == 0 && rendered.executorReport.TruncatedDescriptionChars == 0
}

func combinedSkillsRenderIsBetter(candidate *combinedAvailableSkillsRender, current *combinedAvailableSkillsRender, budget SkillMetadataBudget) bool {
	if candidate.executorReport.IncludedCount != current.executorReport.IncludedCount {
		return candidate.executorReport.IncludedCount > current.executorReport.IncludedCount
	}
	candidateIncluded := candidate.hostReport.IncludedCount + candidate.executorReport.IncludedCount
	currentIncluded := current.hostReport.IncludedCount + current.executorReport.IncludedCount
	if candidateIncluded != currentIncluded {
		return candidateIncluded > currentIncluded
	}
	candidateTruncated := candidate.hostReport.TruncatedDescriptionChars + candidate.executorReport.TruncatedDescriptionChars
	currentTruncated := current.hostReport.TruncatedDescriptionChars + current.executorReport.TruncatedDescriptionChars
	if candidateTruncated != currentTruncated {
		return candidateTruncated < currentTruncated
	}
	return combinedSkillsCost(candidate, budget) < combinedSkillsCost(current, budget)
}

func combinedSkillsCost(rendered *combinedAvailableSkillsRender, budget SkillMetadataBudget) int {
	return availableSkillsCost(budget, rendered.hostSkillRootLines, rendered.hostSkillLines) + linesCost(budget, rendered.executorSkillLines)
}

func extensionOmissionMarker(omitted int) string {
	word := "skills"
	if omitted == 1 {
		word = "skill"
	}
	return fmt.Sprintf("- %d additional %s omitted from this bounded skills list.", omitted, word)
}

func normalizedSkillMetadataBudget(budget SkillMetadataBudget) SkillMetadataBudget {
	if budget.Limit <= 0 {
		return SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget}
	}
	return budget
}

func buildSkillAliasPlan(lines []skillRenderLine, budget SkillMetadataBudget) (*skillAliasPlan, bool) {
	aliasLines := append([]skillRenderLine(nil), lines...)
	sort.SliceStable(aliasLines, func(i int, j int) bool {
		if aliasLines[i].hasRootOrder != aliasLines[j].hasRootOrder {
			return aliasLines[i].hasRootOrder
		}
		if aliasLines[i].hasRootOrder && aliasLines[i].rootOrder != aliasLines[j].rootOrder {
			return aliasLines[i].rootOrder < aliasLines[j].rootOrder
		}
		return false
	})
	usedRoots := make([]string, 0, len(lines))
	for _, line := range aliasLines {
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

// buildExtensionAliasPlan builds an alias plan for executor/orchestrator skill
// locators using provider-specific `e`/`o` prefixes (Rust ce22ea9712 aliases.rs).
// Locator roots are derived from the locator path's leading URI segments.
func buildExtensionAliasPlan(lines []skillRenderLine, budget SkillMetadataBudget) (*skillAliasPlan, bool) {
	usedRoots := make([]string, 0, len(lines))
	seen := map[string]bool{}
	for _, line := range lines {
		root := extensionSkillAliasRoot(line.path)
		if root == "" || seen[root] {
			continue
		}
		if _, ok := extensionSkillPathRelativeToRoot(line.path, root); !ok {
			continue
		}
		seen[root] = true
		usedRoots = append(usedRoots, root)
	}
	if len(usedRoots) == 0 {
		return nil, false
	}
	rootAliases := make(map[string]string, len(usedRoots))
	rootLines := make([]string, 0, len(usedRoots))
	for i, root := range usedRoots {
		alias := fmt.Sprintf("e%d", i)
		rootAliases[root] = alias
		rootLines = append(rootLines, fmt.Sprintf("- `%s` = `%s`", alias, root))
	}
	aliasRootByPath := make(map[string]string, len(lines))
	for _, line := range lines {
		root := extensionSkillAliasRoot(line.path)
		if root == "" {
			continue
		}
		if _, ok := extensionSkillPathRelativeToRoot(line.path, root); !ok {
			continue
		}
		aliasRootByPath[line.path] = root
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

// extensionSkillAliasRoot derives the alias root for an extension locator from
// its leading URI segments (Rust executor alias_root from discovery paths).
func extensionSkillAliasRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	scheme := ""
	trimmed := path
	if schemeIndex := strings.Index(trimmed, "://"); schemeIndex >= 0 {
		scheme = trimmed[:schemeIndex+3]
		trimmed = trimmed[schemeIndex+3:]
		if strings.HasPrefix(trimmed, "/") {
			scheme += "/"
			trimmed = strings.TrimLeft(trimmed, "/")
		}
	}
	segments := strings.Split(trimmed, "/")
	if len(segments) < 3 {
		return ""
	}
	return scheme + segments[0] + "/" + segments[1]
}

func applySkillAliases(lines []skillRenderLine, plan *skillAliasPlan) []skillRenderLine {
	if plan == nil {
		return lines
	}
	out := make([]skillRenderLine, 0, len(lines))
	for _, line := range lines {
		if aliasRoot := plan.aliasRootByPath[line.path]; aliasRoot != "" {
			if alias := plan.rootAliases[aliasRoot]; alias != "" {
				if relative, ok := extensionSkillPathRelativeToRoot(line.path, aliasRoot); ok {
					line.path = alias + "/" + relative
				}
			}
		}
		out = append(out, line)
	}
	return out
}

// extensionSkillPathRelativeToRoot computes the path relative to an extension
// alias root, tolerating a URI scheme prefix (Rust aliases.rs shorten).
func extensionSkillPathRelativeToRoot(path string, root string) (string, bool) {
	root = strings.TrimSpace(root)
	path = strings.TrimSpace(path)
	if root == "" || path == "" {
		return "", false
	}
	scheme := ""
	if index := strings.Index(path, "://"); index >= 0 {
		scheme = path[:index+3]
		path = path[index+3:]
	}
	if scheme != "" && strings.HasPrefix(root, scheme) {
		root = strings.TrimPrefix(root, scheme)
	}
	path = strings.Trim(path, "/")
	root = strings.Trim(root, "/")
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
	if containsSkillPackageLocator(skillLines) {
		builder.WriteString("Read a skill package directly with `skills.read({\"package\":\"<package>\"})` to read its `SKILL.md`; root aliases are resolved automatically. To read another file from that skill, use the same `package` and pass the file's complete `skill://` identifier as `resource`. If the package is not provided, use `skills.list` to find it.\n")
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

func containsSkillPackageLocator(skillLines []string) bool {
	for _, line := range skillLines {
		if strings.Contains(line, "(executor package: ") || strings.Contains(line, "(orchestrator package: ") {
			return true
		}
	}
	return false
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
		if budget.Kind == SkillMetadataBudgetTokens && !budget.Configured {
			prefix = fmt.Sprintf("Exceeded skills context budget of %d%%.", SkillMetadataContextWindowPercent)
		}
		message := fmt.Sprintf("%s All skill descriptions were removed and %d additional %s %s not included in the model-visible skills list.", prefix, report.OmittedCount, word, verb)
		return &message
	}
	if report.TotalCount > 0 && (report.TruncatedDescriptionChars+report.TotalCount-1)/report.TotalCount > SkillDescriptionWarningThreshold {
		message := SkillDescriptionTruncatedWarning
		if budget.Kind == SkillMetadataBudgetTokens && !budget.Configured {
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
