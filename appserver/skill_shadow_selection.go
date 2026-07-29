package appserver

import (
	"strconv"
	"strings"

	"codex_go/config"
	promptctx "codex_go/prompt"
	"codex_go/turn"
)

const (
	skillShadowRunMetric       = "codex.skills.shadow_selection"
	skillShadowDurationMetric  = "codex.skills.shadow_selection.duration_ms"
	skillShadowCatalogMetric   = "codex.skills.shadow_selection.catalog_entries"
	skillShadowSelectedMetric  = "codex.skills.shadow_selection.selected_entries"
	skillShadowTermsMetric     = "codex.skills.shadow_selection.query_terms"
	skillShadowReductionMetric = "codex.skills.shadow_selection.reduction_bps"
	skillShadowMaxQueryBytes   = 16 * 1024
)

func (r *RuntimeRouter) runSkillShadowSelection(cfg *config.Config, params *turn.TurnStartParams, groups ...[]promptctx.InstructionsSkillMetadata) {
	if r == nil || r.services.SkillShadowMetrics == nil || cfg == nil || !cfg.SkillShadowSelectionEnabled() {
		return
	}
	query, queryTruncated := skillShadowQuery(params)
	explicitlySelected := skillShadowExplicitNames(params)
	documents := make([]promptctx.SkillSelectionDocument, 0)
	for _, group := range groups {
		for _, skill := range group {
			if !skill.AllowsImplicitInvocation() || explicitlySelected[strings.ToLower(strings.TrimSpace(skill.Name))] {
				continue
			}
			documents = append(documents, promptctx.SkillSelectionDocument{
				ID: len(documents), Name: skill.Name, ShortDescription: skill.ShortDescription,
				Description: skill.Description, RoutingMetadata: skill.RoutingMetadata,
				Dependencies: skillShadowDependencies(skill.Dependencies),
			})
		}
	}
	observations := promptctx.RunSkillShadowSelection(query, documents)
	for _, observation := range observations {
		status := "selected"
		if observation.QueryTermCount == 0 {
			status = "no_query_terms"
		} else if len(observation.CandidateIDs) == 0 {
			status = "no_matches"
		}
		tags := map[string]string{
			"method": observation.Method, "status": status, "query_script": observation.QueryScript,
			"query_truncated":         strconv.FormatBool(queryTruncated || observation.QueryTruncated),
			"candidate_set_truncated": strconv.FormatBool(observation.CandidateSetTruncated),
		}
		r.services.SkillShadowMetrics.Counter(skillShadowRunMetric, 1, tags)
		r.services.SkillShadowMetrics.RecordDuration(skillShadowDurationMetric, observation.Duration, tags)
		r.services.SkillShadowMetrics.Histogram(skillShadowCatalogMetric, len(documents), tags)
		r.services.SkillShadowMetrics.Histogram(skillShadowSelectedMetric, len(observation.CandidateIDs), tags)
		r.services.SkillShadowMetrics.Histogram(skillShadowTermsMetric, observation.QueryTermCount, tags)
		r.services.SkillShadowMetrics.Histogram(skillShadowReductionMetric, skillShadowReductionBPS(len(documents), len(observation.CandidateIDs)), tags)
	}
}

func skillShadowExplicitNames(params *turn.TurnStartParams) map[string]bool {
	out := map[string]bool{}
	if params == nil {
		return out
	}
	for _, input := range params.Input {
		if input.Type != "skill" && input.Type != "mention" {
			continue
		}
		if name := strings.ToLower(strings.TrimSpace(input.Name)); name != "" {
			out[name] = true
		}
	}
	return out
}

func skillShadowDependencies(dependencies []promptctx.InstructionsSkillDependency) string {
	parts := make([]string, 0, min(len(dependencies), 32)*2)
	for _, dependency := range dependencies[:min(len(dependencies), 32)] {
		parts = append(parts, nonEmptyStrings(dependency.Value, dependency.Description)...)
	}
	return strings.Join(parts, " ")
}

func skillShadowQuery(params *turn.TurnStartParams) (string, bool) {
	if params == nil {
		return "", false
	}
	parts := make([]string, 0, len(params.Input)+1)
	if strings.TrimSpace(params.Prompt) != "" {
		parts = append(parts, params.Prompt)
	}
	for _, input := range params.Input {
		part := input.Text
		if strings.TrimSpace(part) == "" && (input.Type == "skill" || input.Type == "mention") {
			part = input.Name
		}
		if strings.TrimSpace(part) != "" {
			parts = append(parts, part)
		}
	}
	query := strings.Join(parts, " ")
	if len(query) <= skillShadowMaxQueryBytes {
		return query, false
	}
	end := skillShadowMaxQueryBytes
	for end > 0 && query[end]&0xc0 == 0x80 {
		end--
	}
	return query[:end], true
}

func skillShadowReductionBPS(catalog, selected int) int {
	if catalog <= 0 {
		return 0
	}
	return 10000 - selected*10000/catalog
}

func selectSkillMetadata(cfg *config.Config, params *turn.TurnStartParams, skills []promptctx.InstructionsSkillMetadata) []promptctx.InstructionsSkillMetadata {
	if cfg == nil || !cfg.SkillSelectionEnabled() || len(skills) == 0 {
		return skills
	}
	query, _ := skillShadowQuery(params)
	documents := make([]promptctx.SkillSelectionDocument, 0, len(skills))
	for i, skill := range skills {
		if skill.AllowsImplicitInvocation() {
			documents = append(documents, promptctx.SkillSelectionDocument{ID: i, Name: skill.Name, ShortDescription: skill.Description, Description: skill.Description})
		}
	}
	selection := promptctx.SelectSkillsMultiQueryLexical(query, documents, promptctx.SkillShadowSelectionMaxResults)
	if selection.QueryTermCount == 0 {
		return skills
	}
	selected := make(map[int]bool, len(selection.CandidateIDs))
	for _, id := range selection.CandidateIDs {
		selected[id] = true
	}
	out := make([]promptctx.InstructionsSkillMetadata, 0, len(selection.CandidateIDs))
	for i, skill := range skills {
		if selected[i] || !skill.AllowsImplicitInvocation() {
			out = append(out, skill)
		}
	}
	return out
}
