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

	SkillDescriptionTruncatedWarning = "Skill descriptions were shortened to fit the skills context budget. Codex can still see every skill, but some descriptions are shorter. Disable unused skills or plugins to leave more room for the rest."
)

type SkillMetadataBudgetKind string

const (
	SkillMetadataBudgetCharacters SkillMetadataBudgetKind = "characters"
	SkillMetadataBudgetTokens     SkillMetadataBudgetKind = "tokens"
)

type SkillMetadataBudget struct {
	Kind  SkillMetadataBudgetKind
	Limit int
}

type AvailableSkills struct {
	Body           string
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
	scope       string
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

func RenderAvailableSkills(skills []InstructionsSkillMetadata, budget SkillMetadataBudget) *AvailableSkills {
	lines := orderedSkillRenderLines(skills)
	if len(lines) == 0 {
		return nil
	}
	if budget.Limit <= 0 {
		budget = SkillMetadataBudget{Kind: SkillMetadataBudgetCharacters, Limit: DefaultSkillMetadataCharBudget}
	}
	skillLines, report := renderSkillLines(lines, budget)
	warning := skillRenderWarning(report, budget)
	return &AvailableSkills{
		Body:           renderAvailableSkillsBody(skillLines),
		SkillLines:     skillLines,
		Report:         report,
		WarningMessage: warning,
	}
}

func orderedSkillRenderLines(skills []InstructionsSkillMetadata) []skillRenderLine {
	lines := make([]skillRenderLine, 0, len(skills))
	for _, skill := range skills {
		if !skill.AllowsImplicitInvocation() {
			continue
		}
		lines = append(lines, skillRenderLine{
			name:        skill.Name,
			description: truncateSkillDescription(skill.Description),
			path:        strings.ReplaceAll(skill.Path, "\\", "/"),
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

func renderAvailableSkillsBody(skillLines []string) string {
	var builder strings.Builder
	builder.WriteString("\n## Skills\n")
	builder.WriteString("A skill is a set of instructions provided through a `SKILL.md` source. Below is the list of skills that can be used. Each entry includes a name, description, and source locator.\n")
	builder.WriteString("### Available skills\n")
	builder.WriteString(strings.Join(skillLines, "\n"))
	builder.WriteString("\n")
	return builder.String()
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
	label := "file"
	if strings.EqualFold(l.scope, "environment") {
		label = "environment"
	}
	if description == "" {
		return fmt.Sprintf("- %s: (%s: %s)", l.name, label, path)
	}
	return fmt.Sprintf("- %s: %s (%s: %s)", l.name, description, label, path)
}

func truncateSkillDescription(description string) string {
	description = strings.Join(strings.Fields(description), " ")
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
