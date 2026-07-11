package mentionsv2

import "strings"

// Rust parity subset: codex-rs/tui/src/bottom_pane/mentions_v2/search_catalog.rs.

type SearchCatalog struct {
	Candidates []Candidate
}

type SkillMetadata struct {
	Name        string
	DisplayName string
	Description string
	Path        string
}

type PluginCapabilitySummary struct {
	ConfigName      string
	DisplayName     string
	Description     string
	HasSkills       bool
	MCPServerNames  []string
	AppConnectorIDs []string
}

func BuildSearchCatalog(skills []SkillMetadata, plugins []PluginCapabilitySummary) []Candidate {
	candidates := []Candidate{}
	for _, skill := range skills {
		candidates = append(candidates, SkillCandidate(skill))
	}
	for _, plugin := range plugins {
		candidates = append(candidates, PluginCandidate(plugin))
	}
	return candidates
}

func NewSearchCatalog(skills []SkillMetadata, plugins []PluginCapabilitySummary) SearchCatalog {
	return SearchCatalog{Candidates: BuildSearchCatalog(skills, plugins)}
}

func SkillCandidate(skill SkillMetadata) Candidate {
	displayName := SkillDisplayName(skill)
	searchTerms := []string{skill.Name}
	if displayName != skill.Name {
		searchTerms = append(searchTerms, displayName)
	}
	return Candidate{
		ID:          skill.Name,
		Label:       displayName,
		DisplayName: displayName,
		Description: strings.TrimSpace(skill.Description),
		SearchTerms: searchTerms,
		MentionType: MentionTypeSkill,
		Selection:   ToolSelection("$"+skill.Name, emptyToDefault(skill.Path, "")),
	}
}

func SkillDisplayName(skill SkillMetadata) string {
	if skill.DisplayName != "" {
		return skill.DisplayName
	}
	pluginName, skillName, ok := strings.Cut(skill.Name, ":")
	if ok && pluginName != "" && skillName != "" {
		return skillName + " (" + pluginName + ")"
	}
	return skill.Name
}

func PluginCandidate(plugin PluginCapabilitySummary) Candidate {
	pluginName, marketplaceName, _ := strings.Cut(plugin.ConfigName, "@")
	if pluginName == "" {
		pluginName = plugin.ConfigName
	}
	mentionName := PluginMentionName(pluginName, plugin.DisplayName)
	searchTerms := []string{pluginName, plugin.ConfigName}
	if plugin.DisplayName != pluginName {
		searchTerms = append(searchTerms, plugin.DisplayName)
	}
	if marketplaceName != "" {
		searchTerms = append(searchTerms, marketplaceName)
	}
	return Candidate{
		ID:          plugin.ConfigName,
		Label:       plugin.DisplayName,
		DisplayName: plugin.DisplayName,
		Description: PluginDescription(plugin),
		SearchTerms: searchTerms,
		MentionType: MentionTypePlugin,
		Selection:   ToolSelection("@"+mentionName, "plugin://"+plugin.ConfigName),
	}
}

func PluginMentionName(pluginName string, displayName string) string {
	pluginSegments := SplitPluginNameSegments(pluginName)
	displaySegments := SplitDisplayNameSegments(displayName)
	if len(pluginSegments) == len(displaySegments) {
		matches := true
		for idx := range pluginSegments {
			if !strings.EqualFold(pluginSegments[idx].Text, displaySegments[idx]) {
				matches = false
				break
			}
		}
		if matches {
			var b strings.Builder
			for idx, segment := range pluginSegments {
				b.WriteString(displaySegments[idx])
				if segment.Separator != 0 {
					b.WriteRune(segment.Separator)
				}
			}
			return b.String()
		}
	}
	return TitleCasePluginName(pluginName)
}

type PluginNameSegment struct {
	Text      string
	Separator rune
}

func SplitPluginNameSegments(pluginName string) []PluginNameSegment {
	segments := []PluginNameSegment{}
	var current strings.Builder
	for _, r := range pluginName {
		if r == '-' || r == '_' {
			if current.Len() > 0 {
				segments = append(segments, PluginNameSegment{Text: current.String(), Separator: r})
				current.Reset()
			}
			continue
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		segments = append(segments, PluginNameSegment{Text: current.String()})
	}
	return segments
}

func SplitDisplayNameSegments(displayName string) []string {
	fields := strings.FieldsFunc(displayName, func(r rune) bool {
		return !(r >= '0' && r <= '9') && !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z')
	})
	out := []string{}
	for _, field := range fields {
		if field != "" {
			out = append(out, field)
		}
	}
	return out
}

func TitleCasePluginName(pluginName string) string {
	var b strings.Builder
	capitalizeNext := true
	for _, r := range pluginName {
		if r == '-' || r == '_' {
			capitalizeNext = true
			b.WriteRune(r)
			continue
		}
		if capitalizeNext && r >= 'a' && r <= 'z' {
			r = r - 'a' + 'A'
		}
		b.WriteRune(r)
		capitalizeNext = false
	}
	return b.String()
}

func PluginDescription(plugin PluginCapabilitySummary) string {
	if plugin.Description != "" {
		return plugin.Description
	}
	labels := PluginCapabilityLabels(plugin)
	if len(labels) == 0 {
		return "Plugin"
	}
	return "Plugin - " + strings.Join(labels, " - ")
}

func PluginCapabilityLabels(plugin PluginCapabilitySummary) []string {
	labels := []string{}
	if plugin.HasSkills {
		labels = append(labels, "skills")
	}
	if len(plugin.MCPServerNames) == 1 {
		labels = append(labels, "1 MCP server")
	} else if len(plugin.MCPServerNames) > 1 {
		labels = append(labels, formatInt(len(plugin.MCPServerNames))+" MCP servers")
	}
	if len(plugin.AppConnectorIDs) == 1 {
		labels = append(labels, "1 app")
	} else if len(plugin.AppConnectorIDs) > 1 {
		labels = append(labels, formatInt(len(plugin.AppConnectorIDs))+" apps")
	}
	return labels
}

func emptyToDefault(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func formatInt(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}
