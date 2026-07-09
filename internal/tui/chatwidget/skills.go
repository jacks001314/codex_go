package chatwidget

import (
	"net/url"
	"strings"

	"codex_go/internal/apps"
	"codex_go/internal/appserver"
)

const SkillsMenuViewID = "skills-menu"

const (
	SkillsMenuActionList   UsageMenuAction = "skills_list"
	SkillsMenuActionManage UsageMenuAction = "skills_manage"
)

type SkillsToggleItem struct {
	Name        string
	SkillName   string
	Description string
	Enabled     bool
	Path        string
}

type ManageSkillsViewModel struct {
	Items        []SkillsToggleItem
	EmptyMessage string
}

type ToolMentions struct {
	Names       map[string]bool
	LinkedPaths map[string]string
}

func OpenSkillsListInsert(mentionsV2 bool) string {
	if mentionsV2 {
		return "@"
	}
	return "$"
}

func NewSkillsMenuView(mentionsV2 bool) SelectionView {
	listShortcut := OpenSkillsListInsert(mentionsV2)
	return SelectionView{
		ViewID:      SkillsMenuViewID,
		Title:       "Skills",
		Subtitle:    "Choose an action",
		FooterHint:  standardPopupHintLine,
		AllowCancel: true,
		Items: []SelectionItem{
			{
				Name:            "List skills",
				Description:     "Tip: press " + listShortcut + " to open this list directly.",
				Action:          SkillsMenuActionList,
				DismissOnSelect: true,
			},
			{
				Name:            "Enable/Disable Skills",
				Description:     "Enable or disable skills.",
				Action:          SkillsMenuActionManage,
				DismissOnSelect: true,
			},
		},
	}
}

func NewManageSkillsView(skills []appserver.SkillsListEntry) ManageSkillsViewModel {
	if len(skills) == 0 {
		return ManageSkillsViewModel{EmptyMessage: "No skills available."}
	}
	items := make([]SkillsToggleItem, 0, len(skills))
	for _, skill := range skills {
		path := strings.TrimSpace(skill.Path)
		if path == "" || strings.TrimSpace(skill.Name) == "" {
			continue
		}
		items = append(items, SkillsToggleItem{
			Name:        SkillDisplayName(skill),
			SkillName:   strings.TrimSpace(skill.Name),
			Description: SkillDescription(skill),
			Enabled:     skill.Enabled,
			Path:        path,
		})
	}
	if len(items) == 0 {
		return ManageSkillsViewModel{EmptyMessage: "No skills available."}
	}
	return ManageSkillsViewModel{Items: items}
}

func NewSkillsBrowserView(response appserver.SkillsListResponse, cwd string) SelectionView {
	skills, errors := skillsBrowserEntries(response, cwd)
	items := make([]SelectionItem, 0, len(skills)+len(errors))
	for _, skill := range skills {
		id := strings.TrimSpace(skill.Path)
		if id == "" {
			id = strings.TrimSpace(skill.Name)
		}
		if id == "" {
			continue
		}
		status := "disabled"
		if skill.Enabled {
			status = "enabled"
		}
		descriptionParts := []string{status}
		if desc := SkillDescription(skill); desc != "" {
			descriptionParts = append(descriptionParts, desc)
		}
		if strings.TrimSpace(skill.Scope) != "" {
			descriptionParts = append(descriptionParts, "scope: "+strings.TrimSpace(skill.Scope))
		}
		if strings.TrimSpace(skill.PluginID) != "" {
			descriptionParts = append(descriptionParts, "plugin: "+strings.TrimSpace(skill.PluginID))
		}
		if strings.TrimSpace(skill.Path) != "" {
			descriptionParts = append(descriptionParts, strings.TrimSpace(skill.Path))
		}
		items = append(items, SelectionItem{
			ID:          id,
			Name:        firstNonEmptyRequestID(SkillDisplayName(skill), id),
			Description: strings.Join(descriptionParts, "\n"),
			SearchValue: strings.Join([]string{
				skill.Name,
				SkillDisplayName(skill),
				SkillDescription(skill),
				skill.Scope,
				skill.PluginID,
				skill.Path,
			}, " "),
		})
	}
	for i, skillErr := range errors {
		message := strings.TrimSpace(skillErr.Message)
		path := strings.TrimSpace(skillErr.Path)
		if message == "" && path == "" {
			continue
		}
		items = append(items, SelectionItem{
			ID:          "skill-error-" + intString(i+1),
			Name:        "Skill error",
			Description: strings.TrimSpace(strings.Join([]string{path, message}, "\n")),
			Disabled:    true,
		})
	}
	if len(items) == 0 {
		items = append(items, SelectionItem{Name: "No skills available", Disabled: true})
	}
	header := []string{skillsBrowserCountLine(skills)}
	return SelectionView{
		ViewID:            "skills-browser",
		Title:             "Skills",
		HeaderLines:       header,
		FooterHint:        standardPopupHintLine,
		AllowCancel:       true,
		Searchable:        true,
		SearchPlaceholder: "Type to search skills",
		Items:             items,
	}
}

func ManageSkillsChangeSummary(initial map[string]bool, current map[string]bool) (enabledCount int, disabledCount int, message string, changed bool) {
	for path, wasEnabled := range initial {
		isEnabled, ok := current[path]
		if !ok || wasEnabled == isEnabled {
			continue
		}
		if isEnabled {
			enabledCount++
		} else {
			disabledCount++
		}
	}
	if enabledCount == 0 && disabledCount == 0 {
		return 0, 0, "", false
	}
	return enabledCount, disabledCount, intString(enabledCount) + " skills enabled, " + intString(disabledCount) + " skills disabled", true
}

func skillsBrowserEntries(response appserver.SkillsListResponse, cwd string) ([]appserver.SkillsListEntry, []appserver.SkillErrorInfo) {
	cwd = strings.TrimSpace(cwd)
	if cwd != "" {
		for _, entry := range response.Data {
			if strings.TrimSpace(entry.CWD) == cwd {
				return cloneSkillEntries(entry.Skills), append([]appserver.SkillErrorInfo(nil), entry.Errors...)
			}
		}
	}
	skills := []appserver.SkillsListEntry{}
	errors := []appserver.SkillErrorInfo{}
	if len(response.Data) == 0 {
		return cloneSkillEntries(response.Skills), nil
	}
	for _, entry := range response.Data {
		skills = append(skills, cloneSkillEntries(entry.Skills)...)
		errors = append(errors, entry.Errors...)
	}
	return skills, errors
}

func skillsBrowserCountLine(skills []appserver.SkillsListEntry) string {
	enabled := 0
	for _, skill := range skills {
		if skill.Enabled {
			enabled++
		}
	}
	return intString(enabled) + " enabled of " + intString(len(skills)) + " skills."
}

func SkillsForCWD(cwd string, response *appserver.SkillsListResponse) []appserver.SkillsListEntry {
	if response == nil {
		return nil
	}
	for _, entry := range response.Data {
		if strings.TrimSpace(entry.CWD) == strings.TrimSpace(cwd) {
			return cloneSkillEntries(entry.Skills)
		}
	}
	return nil
}

func EnabledSkillsForMentions(skills []appserver.SkillsListEntry) []appserver.SkillsListEntry {
	out := make([]appserver.SkillsListEntry, 0, len(skills))
	for _, skill := range skills {
		if skill.Enabled {
			out = append(out, cloneSkillEntry(skill))
		}
	}
	return out
}

func SkillDisplayName(skill appserver.SkillsListEntry) string {
	if skill.Interface != nil && strings.TrimSpace(skill.Interface.DisplayName) != "" {
		return strings.TrimSpace(skill.Interface.DisplayName)
	}
	name := strings.TrimSpace(skill.Name)
	if pluginName, skillName, ok := strings.Cut(name, ":"); ok {
		pluginName = strings.TrimSpace(pluginName)
		skillName = strings.TrimSpace(skillName)
		if pluginName != "" && skillName != "" {
			return skillName + " (" + pluginName + ")"
		}
	}
	return name
}

func SkillDescription(skill appserver.SkillsListEntry) string {
	if skill.Interface != nil && strings.TrimSpace(skill.Interface.ShortDescription) != "" {
		return strings.TrimSpace(skill.Interface.ShortDescription)
	}
	if strings.TrimSpace(skill.ShortDescription) != "" {
		return strings.TrimSpace(skill.ShortDescription)
	}
	return strings.TrimSpace(skill.Description)
}

func CollectToolMentions(text string, mentionPaths map[string]string) ToolMentions {
	mentions := ExtractToolMentionsFromText(text)
	for name, path := range mentionPaths {
		name = strings.TrimSpace(name)
		path = strings.TrimSpace(path)
		if name == "" || path == "" || !mentions.Names[name] {
			continue
		}
		if _, exists := mentions.LinkedPaths[name]; !exists {
			mentions.LinkedPaths[name] = path
		}
	}
	return mentions
}

func ExtractToolMentionsFromText(text string) ToolMentions {
	return ExtractToolMentionsFromTextWithSigil(text, '$')
}

func ExtractToolMentionsFromTextWithSigil(text string, sigil byte) ToolMentions {
	mentions := ToolMentions{
		Names:       map[string]bool{},
		LinkedPaths: map[string]string{},
	}
	index := 0
	for index < len(text) {
		if text[index] == '[' {
			if name, path, end, ok := parseLinkedToolMention(text, index, sigil); ok {
				if !isCommonEnvVar(name) {
					if IsSkillMentionPath(path) {
						mentions.Names[name] = true
					}
					if _, exists := mentions.LinkedPaths[name]; !exists {
						mentions.LinkedPaths[name] = path
					}
				}
				index = end
				continue
			}
		}
		if text[index] != sigil {
			index++
			continue
		}
		nameStart := index + 1
		if nameStart >= len(text) || !isMentionNameChar(text[nameStart]) {
			index++
			continue
		}
		nameEnd := nameStart + 1
		for nameEnd < len(text) && isMentionNameChar(text[nameEnd]) {
			nameEnd++
		}
		name := text[nameStart:nameEnd]
		if !isCommonEnvVar(name) {
			mentions.Names[name] = true
		}
		index = nameEnd
	}
	return mentions
}

func FindSkillMentions(mentions ToolMentions, skills []appserver.SkillsListEntry) []appserver.SkillsListEntry {
	mentionSkillPaths := map[string]bool{}
	for _, path := range mentions.LinkedPaths {
		if IsSkillMentionPath(path) {
			mentionSkillPaths[NormalizeSkillMentionPath(path)] = true
		}
	}
	seenNames := map[string]bool{}
	seenPaths := map[string]bool{}
	matches := []appserver.SkillsListEntry{}
	for _, skill := range skills {
		path := NormalizeSkillMentionPath(skill.Path)
		if path == "" || seenPaths[path] || !mentionSkillPaths[path] {
			continue
		}
		seenPaths[path] = true
		seenNames[skill.Name] = true
		matches = append(matches, cloneSkillEntry(skill))
	}
	for _, skill := range skills {
		path := NormalizeSkillMentionPath(skill.Path)
		if path == "" || seenPaths[path] {
			continue
		}
		name := strings.TrimSpace(skill.Name)
		if name != "" && mentions.Names[name] && !seenNames[name] {
			seenPaths[path] = true
			seenNames[name] = true
			matches = append(matches, cloneSkillEntry(skill))
		}
	}
	return matches
}

func FindAppMentions(mentions ToolMentions, appList []apps.AppEntry, skillNamesLower map[string]bool) []apps.AppEntry {
	explicitNames := map[string]bool{}
	selectedIDs := map[string]bool{}
	for name, path := range mentions.LinkedPaths {
		if appID := AppIDFromMentionPath(path); appID != "" {
			explicitNames[name] = true
			selectedIDs[appID] = true
		}
	}
	slugCounts := map[string]int{}
	for _, app := range appList {
		if !IsAppMentionable(app) {
			continue
		}
		slug := appMentionSlug(app)
		if slug != "" {
			slugCounts[slug]++
		}
	}
	for _, app := range appList {
		if !IsAppMentionable(app) {
			continue
		}
		slug := appMentionSlug(app)
		if slug == "" {
			continue
		}
		if mentions.Names[slug] && !explicitNames[slug] && slugCounts[slug] == 1 && !skillNamesLower[strings.ToLower(slug)] {
			selectedIDs[app.ID] = true
		}
	}
	out := []apps.AppEntry{}
	for _, app := range appList {
		if IsAppMentionable(app) && selectedIDs[strings.TrimSpace(app.ID)] {
			out = append(out, app)
		}
	}
	return out
}

func IsAppMentionable(app apps.AppEntry) bool {
	return strings.TrimSpace(app.ID) != "" && app.IsAccessible && (app.IsEnabled || app.Enabled)
}

func SkillNamesLower(skills []appserver.SkillsListEntry) map[string]bool {
	out := map[string]bool{}
	for _, skill := range skills {
		if name := strings.TrimSpace(skill.Name); name != "" {
			out[strings.ToLower(name)] = true
		}
	}
	return out
}

func IsSkillMentionPath(path string) bool {
	path = strings.TrimSpace(path)
	return !strings.HasPrefix(path, "app://") && !strings.HasPrefix(path, "mcp://") && !strings.HasPrefix(path, "plugin://")
}

func NormalizeSkillMentionPath(path string) string {
	path = strings.TrimSpace(path)
	return strings.TrimPrefix(path, "skill://")
}

func AppIDFromMentionPath(path string) string {
	path = strings.TrimSpace(path)
	value := strings.TrimPrefix(path, "app://")
	if value == path || value == "" {
		return ""
	}
	if unescaped, err := url.PathUnescape(value); err == nil {
		value = unescaped
	}
	return strings.TrimSpace(value)
}

func parseLinkedToolMention(text string, start int, sigil byte) (string, string, int, bool) {
	sigilIndex := start + 1
	if sigilIndex >= len(text) || text[sigilIndex] != sigil {
		return "", "", start + 1, false
	}
	nameStart := sigilIndex + 1
	if nameStart >= len(text) || !isMentionNameChar(text[nameStart]) {
		return "", "", start + 1, false
	}
	nameEnd := nameStart + 1
	for nameEnd < len(text) && isMentionNameChar(text[nameEnd]) {
		nameEnd++
	}
	if nameEnd >= len(text) || text[nameEnd] != ']' {
		return "", "", start + 1, false
	}
	pathStart := nameEnd + 1
	for pathStart < len(text) && isASCIIWhitespace(text[pathStart]) {
		pathStart++
	}
	if pathStart >= len(text) || text[pathStart] != '(' {
		return "", "", start + 1, false
	}
	pathEnd := pathStart + 1
	for pathEnd < len(text) && text[pathEnd] != ')' {
		pathEnd++
	}
	if pathEnd >= len(text) || text[pathEnd] != ')' {
		return "", "", start + 1, false
	}
	path := strings.TrimSpace(text[pathStart+1 : pathEnd])
	if path == "" {
		return "", "", start + 1, false
	}
	return text[nameStart:nameEnd], path, pathEnd + 1, true
}

func isCommonEnvVar(name string) bool {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "PATH", "HOME", "USER", "SHELL", "PWD", "TMPDIR", "TEMP", "TMP", "LANG", "TERM", "XDG_CONFIG_HOME":
		return true
	default:
		return false
	}
}

func isMentionNameChar(ch byte) bool {
	return ch >= 'a' && ch <= 'z' ||
		ch >= 'A' && ch <= 'Z' ||
		ch >= '0' && ch <= '9' ||
		ch == '_' ||
		ch == '-'
}

func isASCIIWhitespace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

func appMentionSlug(app apps.AppEntry) string {
	name := strings.TrimSpace(app.Name)
	if name == "" {
		name = strings.TrimSpace(app.ID)
	}
	return apps.ConnectorMentionSlugFromName(name)
}

func cloneSkillEntries(skills []appserver.SkillsListEntry) []appserver.SkillsListEntry {
	out := make([]appserver.SkillsListEntry, len(skills))
	for i := range skills {
		out[i] = cloneSkillEntry(skills[i])
	}
	return out
}

func cloneSkillEntry(skill appserver.SkillsListEntry) appserver.SkillsListEntry {
	out := skill
	if skill.Interface != nil {
		value := *skill.Interface
		out.Interface = &value
	}
	if skill.Dependencies != nil {
		dependencies := &appserver.SkillDependencies{Tools: append([]appserver.SkillToolDependency(nil), skill.Dependencies.Tools...)}
		out.Dependencies = dependencies
	}
	out.Errors = append([]appserver.SkillErrorInfo(nil), skill.Errors...)
	out.Skills = cloneSkillEntries(skill.Skills)
	return out
}
