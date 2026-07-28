package tea

import (
	"fmt"
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/appserver"
	"codex_go/features"
	"codex_go/plugin"
	codextui "codex_go/tui"
	mentionsv2 "codex_go/tui/bottom_pane/mentions_v2"
	chatwidget "codex_go/tui/chatwidget"
)

const skillPopupMaxRows = 8

type skillPopupItem struct {
	Name        string
	DisplayName string
	Description string
	Path        string
}

type skillPopupState struct {
	Active   bool
	Query    string
	Items    []skillPopupItem
	Selected int
	Loading  bool
	Err      string
}

func (m *Model) refreshSkillPopup() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	query, ok := skillPopupQuery(m.composer.Value())
	if !ok && features.Enabled(m.featureSettings, "mentions_v2") {
		m.skillPopup = skillPopupState{}
		return m.refreshMentionPopup()
	}
	if !ok {
		m.skillPopup = skillPopupState{}
		m.mentionPopup = nil
		return nil
	}
	m.mentionPopup = nil
	cwd := strings.TrimSpace(m.sessionCWD)
	inventoryReady := m.skillsInventory != nil && m.skillsInventoryCWD == cwd
	inventoryError := m.skillsInventoryErr != "" && m.skillsInventoryCWD == cwd
	if !inventoryReady && !inventoryError {
		m.skillPopup = skillPopupState{
			Active:   true,
			Query:    query,
			Selected: -1,
			Loading:  m.skillsInventoryLoading || m.onReadSkills != nil,
		}
		if m.onReadSkills == nil || m.skillsInventoryLoading {
			return nil
		}
		m.skillsInventoryLoading = true
		reader := m.onReadSkills
		return func() bubbletea.Msg {
			response, err := reader(cwd, false)
			return SkillsInventoryResultMsg{CWD: cwd, Response: response, Err: err}
		}
	}
	items := filterSkillPopupItems(skillPopupItemsFromResponse(*m.skillsInventory, cwd), query)
	selected := 0
	if len(items) == 0 {
		selected = -1
	} else if previous := m.selectedSkillPopupKey(); previous != "" {
		for i, item := range items {
			if skillPopupItemKey(item) == previous {
				selected = i
				break
			}
		}
	}
	m.skillPopup = skillPopupState{
		Active:   true,
		Query:    query,
		Items:    items,
		Selected: selected,
		Err:      m.skillsInventoryErr,
	}
	return nil
}

func (m *Model) refreshMentionPopup() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	start, end, query, ok := m.currentMentionPopupTokenRange()
	if !ok {
		m.mentionPopup = nil
		m.mentionDismissedToken = ""
		return nil
	}
	tokenKey := mentionPopupTokenKey(start, end, query)
	if m.mentionPopup == nil && m.mentionDismissedToken == tokenKey {
		return nil
	}
	if m.mentionDismissedToken != "" && m.mentionDismissedToken != tokenKey {
		m.mentionDismissedToken = ""
	}

	newPopup := m.mentionPopup == nil
	previousQuery := ""
	if m.mentionPopup != nil {
		previousQuery = m.mentionPopup.Query
		m.mentionPopup.SetQuery(query)
		m.mentionPopup.SetCandidates(m.mentionCandidates())
	} else {
		m.mentionPopup = mentionsv2.NewPopupWithQuery(m.mentionCandidates(), query)
	}

	commands := []bubbletea.Cmd{}
	cwd := strings.TrimSpace(m.sessionCWD)
	if !m.skillsInventoryLoading && (m.skillsInventory == nil || m.skillsInventoryCWD != cwd) && m.onReadSkills != nil {
		m.skillsInventoryLoading = true
		reader := m.onReadSkills
		commands = append(commands, func() bubbletea.Msg {
			response, err := reader(cwd, false)
			return SkillsInventoryResultMsg{CWD: cwd, Response: response, Err: err}
		})
	}
	if !m.mentionPluginInventoryReady && !m.mentionPluginInventoryLoading && m.onReadPlugins != nil {
		m.mentionPluginInventoryLoading = true
		reader := m.onReadPlugins
		commands = append(commands, func() bubbletea.Msg {
			response, err := reader(cwd, false)
			return MentionPluginInventoryResultMsg{Response: response, Err: err}
		})
	}
	if m.onFuzzyFileSearch != nil && (newPopup || previousQuery != query) {
		m.mentionFileSearchGeneration++
		generation := m.mentionFileSearchGeneration
		cwd := strings.TrimSpace(m.sessionCWD)
		reader := m.onFuzzyFileSearch
		token := "tui-mentions-v2"
		commands = append(commands, func() bubbletea.Msg {
			if newPopup && query != "" {
				_, _ = reader("", cwd, token)
			}
			response, err := reader(query, cwd, token)
			return MentionFileSearchResultMsg{Generation: generation, Query: query, Matches: response.Files, Err: err}
		})
	}
	return bubbletea.Batch(commands...)
}

func (m *Model) currentMentionPopupTokenRange() (int, int, string, bool) {
	if m == nil {
		return 0, 0, "", false
	}
	text := m.composer.Value()
	lines := strings.Split(text, "\n")
	row := m.composer.Line()
	if row < 0 || row >= len(lines) {
		return 0, 0, "", false
	}
	lineInfo := m.composer.LineInfo()
	cursor := 0
	for i := 0; i < row; i++ {
		cursor += len([]rune(lines[i])) + 1
	}
	cursor += lineInfo.StartColumn + lineInfo.ColumnOffset
	return mentionPopupTokenRangeAtCursor(text, cursor)
}

func mentionPopupTokenRangeAtCursor(text string, cursor int) (int, int, string, bool) {
	runes := []rune(text)
	if cursor < 0 || cursor > len(runes) {
		return 0, 0, "", false
	}
	start := cursor
	for start > 0 && mentionPopupNameRune(runes[start-1]) {
		start--
	}
	end := cursor
	for end < len(runes) && mentionPopupNameRune(runes[end]) {
		end++
	}
	if start == 0 || runes[start-1] != '@' {
		return 0, 0, "", false
	}
	return start - 1, end, string(runes[start:end]), true
}

func mentionPopupNameRune(value rune) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '-' || value == '_'
}

func mentionPopupTokenKey(start int, end int, query string) string {
	return fmt.Sprintf("%d:%d:%s", start, end, query)
}

func (m *Model) mentionCandidates() []mentionsv2.Candidate {
	return mentionsv2.BuildSearchCatalog(m.mentionSkillMetadata(), m.mentionPluginSummaries())
}

func (m *Model) mentionSkillMetadata() []mentionsv2.SkillMetadata {
	if m == nil || m.skillsInventory == nil {
		return nil
	}
	cwd := strings.TrimSpace(m.sessionCWD)
	skills := chatwidget.EnabledSkillsForMentions(chatwidget.SkillsForCWD(cwd, m.skillsInventory))
	if len(skills) == 0 && len(m.skillsInventory.Data) == 0 {
		skills = chatwidget.EnabledSkillsForMentions(m.skillsInventory.Skills)
	}
	out := make([]mentionsv2.SkillMetadata, 0, len(skills))
	for _, skill := range skills {
		out = append(out, mentionsv2.SkillMetadata{
			Name:        strings.TrimSpace(skill.Name),
			DisplayName: chatwidget.SkillDisplayName(skill),
			Description: chatwidget.SkillDescription(skill),
			Path:        strings.TrimSpace(skill.Path),
		})
	}
	return out
}

func (m *Model) mentionPluginSummaries() []mentionsv2.PluginCapabilitySummary {
	if m == nil {
		return nil
	}
	out := make([]mentionsv2.PluginCapabilitySummary, 0, len(m.mentionPluginInventory))
	for _, item := range m.mentionPluginInventory {
		if !item.Installed || !item.Enabled {
			continue
		}
		description, _ := chatwidget.PluginDescription(item)
		out = append(out, mentionsv2.PluginCapabilitySummary{
			ConfigName:      firstNonEmpty(item.ID, item.Name),
			DisplayName:     firstNonEmpty(chatwidget.PluginDisplayName(item), item.Name, item.ID),
			Description:     description,
			HasSkills:       item.HasSkills,
			MCPServerNames:  append([]string(nil), item.MCPServers...),
			AppConnectorIDs: append([]string(nil), item.AppConnectors...),
		})
	}
	return out
}

func pluginSummariesFromResponse(response plugin.PluginListResponse) []plugin.PluginSummary {
	byID := map[string]plugin.PluginSummary{}
	for _, item := range response.Plugins {
		byID[firstNonEmpty(item.ID, item.Name)] = item
	}
	for _, marketplace := range response.Marketplaces {
		for _, item := range marketplace.Plugins {
			byID[firstNonEmpty(item.ID, item.Name)] = item
		}
	}
	out := make([]plugin.PluginSummary, 0, len(byID))
	for _, item := range byID {
		out = append(out, item)
	}
	return out
}

func (m *Model) applyMentionPluginInventoryResult(message MentionPluginInventoryResultMsg) {
	if m == nil {
		return
	}
	m.mentionPluginInventoryLoading = false
	if message.Err != nil {
		m.mentionPluginInventoryErr = strings.TrimSpace(message.Err.Error())
		m.mentionPluginInventoryReady = true
		return
	}
	m.mentionPluginInventory = pluginSummariesFromResponse(message.Response)
	m.mentionPluginInventoryReady = true
	m.mentionPluginInventoryErr = ""
	if m.mentionPopup != nil {
		m.mentionPopup.SetCandidates(m.mentionCandidates())
	}
}

func (m *Model) applyMentionFileSearchResult(message MentionFileSearchResultMsg) {
	if m == nil || m.mentionPopup == nil || message.Generation != m.mentionFileSearchGeneration || message.Query != m.mentionPopup.Query {
		return
	}
	if message.Err != nil {
		m.mentionPopup.SetFileMatches(message.Query, nil)
		return
	}
	m.mentionPopup.SetFileMatches(message.Query, message.Matches)
}

func (m *Model) applySkillsInventoryResult(message SkillsInventoryResultMsg) {
	if m == nil {
		return
	}
	m.skillsInventoryLoading = false
	if message.Err != nil {
		m.skillsInventoryCWD = strings.TrimSpace(message.CWD)
		m.skillsInventoryErr = strings.TrimSpace(message.Err.Error())
		if m.skillsInventoryErr == "" {
			m.skillsInventoryErr = "failed to load skills"
		}
	} else {
		response := message.Response
		m.skillsInventory = &response
		m.skillsInventoryCWD = strings.TrimSpace(message.CWD)
		m.skillsInventoryErr = ""
	}
	m.refreshSkillPopup()
}

func (m *Model) updateSkillPopupKey(msg bubbletea.KeyMsg) (bubbletea.Cmd, bool) {
	if m == nil {
		return nil, false
	}
	if m.mentionPopup != nil {
		return m.updateMentionPopupKey(msg)
	}
	if !m.skillPopup.Active {
		return nil, false
	}
	switch msg.Type {
	case bubbletea.KeyUp, bubbletea.KeyCtrlP:
		m.moveSkillPopupSelection(-1)
		return nil, true
	case bubbletea.KeyDown, bubbletea.KeyCtrlN:
		m.moveSkillPopupSelection(1)
		return nil, true
	case bubbletea.KeyEsc:
		m.skillPopup = skillPopupState{}
		return nil, true
	case bubbletea.KeyEnter, bubbletea.KeyTab:
		if m.insertSelectedSkillPopupItem() {
			return nil, true
		}
	}
	return nil, false
}

func (m *Model) updateMentionPopupKey(msg bubbletea.KeyMsg) (bubbletea.Cmd, bool) {
	if m == nil || m.mentionPopup == nil {
		return nil, false
	}
	switch msg.Type {
	case bubbletea.KeyUp, bubbletea.KeyCtrlP:
		m.mentionPopup.MoveUp()
		return nil, true
	case bubbletea.KeyDown, bubbletea.KeyCtrlN:
		m.mentionPopup.MoveDown()
		return nil, true
	case bubbletea.KeyLeft:
		m.mentionPopup.PreviousSearchMode()
		return nil, true
	case bubbletea.KeyRight:
		m.mentionPopup.NextSearchMode()
		return nil, true
	case bubbletea.KeyEsc:
		if start, end, query, ok := m.currentMentionPopupTokenRange(); ok {
			m.mentionDismissedToken = mentionPopupTokenKey(start, end, query)
		}
		m.mentionPopup = nil
		return nil, true
	case bubbletea.KeyEnter, bubbletea.KeyTab:
		selection, selected := m.mentionPopup.SelectedSelection()
		m.mentionPopup = nil
		if selected {
			m.insertMentionSelection(selection)
			return nil, true
		}
		return nil, msg.Type == bubbletea.KeyTab
	default:
		return nil, false
	}
}

func (m *Model) insertMentionSelection(selection mentionsv2.Selection) {
	if m == nil {
		return
	}
	start, end, _, ok := m.currentMentionPopupTokenRange()
	if !ok {
		return
	}
	insert := strings.TrimSpace(selection.InsertText)
	if selection.Kind == mentionsv2.SelectionFile {
		insert = strings.TrimSpace(selection.Path)
	}
	if insert == "" {
		return
	}
	runes := []rune(m.composer.Value())
	text := string(runes[:start]) + insert + " " + string(runes[end:])
	m.composer.SetValue(text)
	m.composer.CursorEnd()
	if selection.Kind == mentionsv2.SelectionTool {
		m.addComposerMentionBinding(insert + "|" + strings.TrimSpace(selection.Path))
	}
	m.mentionDismissedToken = ""
}

func (m *Model) addComposerMentionBinding(binding string) {
	if m == nil {
		return
	}
	binding = strings.TrimSpace(binding)
	if binding == "" || strings.HasSuffix(binding, "|") {
		return
	}
	for _, existing := range m.composerMentionBindings {
		if existing == binding {
			return
		}
	}
	m.composerMentionBindings = append(m.composerMentionBindings, binding)
}

func (m *Model) moveSkillPopupSelection(delta int) {
	if m == nil || len(m.skillPopup.Items) == 0 {
		return
	}
	next := m.skillPopup.Selected + delta
	switch {
	case next < 0:
		next = len(m.skillPopup.Items) - 1
	case next >= len(m.skillPopup.Items):
		next = 0
	}
	m.skillPopup.Selected = next
}

func (m *Model) insertSelectedSkillPopupItem() bool {
	if m == nil || m.skillPopup.Selected < 0 || m.skillPopup.Selected >= len(m.skillPopup.Items) {
		return false
	}
	item := m.skillPopup.Items[m.skillPopup.Selected]
	insert := "$" + strings.TrimSpace(item.Name)
	if _, _, _, ok := mentionPopupTokenRange(m.composer.Value()); ok {
		insert = "@" + strings.TrimSpace(item.Name)
	}
	if insert == "$" || insert == "@" {
		return false
	}
	text := replaceSkillPopupToken(m.composer.Value(), insert+" ")
	m.composer.SetValue(text)
	m.addComposerSkillMentionBinding(item)
	m.skillPopup = skillPopupState{}
	return true
}

func (m *Model) addComposerSkillMentionBinding(item skillPopupItem) {
	if m == nil {
		return
	}
	binding := skillPopupMentionBinding(item)
	if binding == "" {
		return
	}
	m.addComposerMentionBinding(binding)
}

func skillPopupMentionBinding(item skillPopupItem) string {
	name := strings.TrimSpace(item.Name)
	path := strings.TrimSpace(item.Path)
	if name == "" || path == "" {
		return ""
	}
	if !strings.Contains(path, "://") {
		path = "skill://" + path
	} else if !strings.HasPrefix(path, "skill://") && !strings.HasPrefix(path, "environment://") {
		path = "skill://" + path
	} else if strings.HasPrefix(path, "environment://") {
		path = "skill://" + path
	}
	return name + "|" + path
}

func (m *Model) selectedSkillPopupKey() string {
	if m == nil || !m.skillPopup.Active || m.skillPopup.Selected < 0 || m.skillPopup.Selected >= len(m.skillPopup.Items) {
		return ""
	}
	return skillPopupItemKey(m.skillPopup.Items[m.skillPopup.Selected])
}

func (m *Model) renderSkillPopup() string {
	if m == nil {
		return ""
	}
	if m.mentionPopup != nil {
		width := firstPositive(m.width, defaultWidth)
		height := m.mentionPopup.CalculateRequiredHeight(width)
		return strings.Join(mentionsv2.RenderPopup(m.mentionPopup, width, height), "\n")
	}
	if !m.skillPopup.Active {
		return ""
	}
	width := firstPositive(m.width, defaultWidth)
	if m.skillPopup.Loading {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  Loading skills...")
	}
	if strings.TrimSpace(m.skillPopup.Err) != "" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  Skills: " + fitTerminalLine(m.skillPopup.Err, width-10))
	}
	if len(m.skillPopup.Items) == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render("  no matches")
	}
	visible := slashPopupVisibleRange(len(m.skillPopup.Items), m.skillPopup.Selected, skillPopupMaxRows)
	nameWidth := skillPopupNameWidth(m.skillPopup.Items[visible.start:visible.end])
	lines := []string{}
	for idx := visible.start; idx < visible.end; idx++ {
		item := m.skillPopup.Items[idx]
		selected := idx == m.skillPopup.Selected
		lines = append(lines, skillPopupRenderLine(item, selected, nameWidth, width))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func skillPopupQuery(text string) (string, bool) {
	_, _, query, ok := skillPopupTokenRange(text)
	return query, ok
}

func mentionPopupQuery(text string) (string, bool) {
	_, _, query, ok := mentionPopupTokenRange(text)
	return query, ok
}

func skillPopupTokenRange(text string) (int, int, string, bool) {
	if text == "" {
		return 0, 0, "", false
	}
	if strings.ContainsAny(text[len(text)-1:], " \t\r\n") {
		return 0, 0, "", false
	}
	start := strings.LastIndexAny(text, " \t\r\n") + 1
	if start < 0 || start > len(text) {
		return 0, 0, "", false
	}
	token := text[start:]
	if !strings.HasPrefix(token, "$") {
		return 0, 0, "", false
	}
	return start, len(text), strings.TrimPrefix(token, "$"), true
}

// mentionPopupTokenRange mirrors the Rust mentions_v2 trigger: @ starts a
// tool/plugin mention, while $ remains reserved for skills and apps.
func mentionPopupTokenRange(text string) (int, int, string, bool) {
	text = strings.TrimRight(text, "\r\n")
	end := len(text)
	start := end
	for start > 0 {
		b := text[start-1]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' {
			start--
			continue
		}
		break
	}
	if start == 0 || text[start-1] != '@' {
		return 0, 0, "", false
	}
	return start - 1, end, text[start:end], true
}

func replaceSkillPopupToken(text string, insert string) string {
	start, end, _, ok := skillPopupTokenRange(text)
	if !ok {
		if text != "" && !strings.HasSuffix(text, " ") {
			text += " "
		}
		return text + insert
	}
	return text[:start] + insert + text[end:]
}

func skillPopupItemsFromResponse(response appserver.SkillsListResponse, cwd string) []skillPopupItem {
	skills := chatwidget.EnabledSkillsForMentions(chatwidget.SkillsForCWD(cwd, &response))
	if len(skills) == 0 && len(response.Data) == 0 {
		skills = chatwidget.EnabledSkillsForMentions(response.Skills)
	}
	items := make([]skillPopupItem, 0, len(skills))
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		items = append(items, skillPopupItem{
			Name:        name,
			DisplayName: firstNonEmpty(chatwidget.SkillDisplayName(skill), name),
			Description: chatwidget.SkillDescription(skill),
			Path:        strings.TrimSpace(skill.Path),
		})
	}
	return items
}

func filterSkillPopupItems(items []skillPopupItem, query string) []skillPopupItem {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return append([]skillPopupItem(nil), items...)
	}
	out := []skillPopupItem{}
	for _, item := range items {
		for _, value := range []string{item.Name, item.DisplayName, item.Description} {
			if strings.Contains(strings.ToLower(strings.TrimSpace(value)), query) {
				out = append(out, item)
				break
			}
		}
	}
	return out
}

func skillPopupNameWidth(items []skillPopupItem) int {
	width := 0
	for _, item := range items {
		if n := codextui.DisplayWidth(item.DisplayName); n > width {
			width = n
		}
	}
	switch {
	case width > 28:
		return 28
	case width < 10:
		return 10
	default:
		return width
	}
}

func skillPopupRenderLine(item skillPopupItem, selected bool, nameWidth int, width int) string {
	prefix := codextui.SelectionPrefix(selected)
	availableDescription := width - codextui.DisplayWidth(prefix) - nameWidth - 1
	if availableDescription < 0 {
		availableDescription = 0
	}
	name := codextui.TruncateWithEllipsis(item.DisplayName, nameWidth)
	description := codextui.TruncateWithEllipsis(item.Description, availableDescription)
	rawName := padRightDisplay(name, nameWidth)
	if selected {
		return codextui.RenderSelectedRow(prefix + rawName + " " + description)
	}
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("15"))
	descriptionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	return prefix + nameStyle.Render(rawName) + " " + descriptionStyle.Render(description)
}

func skillPopupItemKey(item skillPopupItem) string {
	if strings.TrimSpace(item.Path) != "" {
		return strings.TrimSpace(item.Path)
	}
	return strings.TrimSpace(item.Name)
}

func (m *Model) activeComposerMentionBindings(text string) []string {
	if m == nil || len(m.composerMentionBindings) == 0 {
		return nil
	}
	mentions := chatwidget.CollectToolMentions(text, nil)
	atMentions := chatwidget.ExtractToolMentionsFromTextWithSigil(text, '@')
	out := make([]string, 0, len(m.composerMentionBindings))
	for _, binding := range m.composerMentionBindings {
		name := mentionBindingName(binding)
		if name == "" || mentions.Names[name] || atMentions.Names[name] {
			out = append(out, binding)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mentionBindingName(binding string) string {
	binding = strings.TrimSpace(binding)
	if binding == "" {
		return ""
	}
	for _, sep := range []string{"|", "\t", "="} {
		if left, _, ok := strings.Cut(binding, sep); ok {
			return strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(left), "$"), "@")
		}
	}
	if strings.Contains(binding, "://") {
		return ""
	}
	return strings.TrimPrefix(strings.TrimPrefix(binding, "$"), "@")
}

func (m *Model) submissionMentionCatalog() chatwidget.SubmissionMentionCatalog {
	if m == nil {
		return chatwidget.SubmissionMentionCatalog{}
	}
	var skills []appserver.SkillsListEntry
	if m.skillsInventory != nil {
		cwd := strings.TrimSpace(m.sessionCWD)
		skills = chatwidget.EnabledSkillsForMentions(chatwidget.SkillsForCWD(cwd, m.skillsInventory))
		if len(skills) == 0 && len(m.skillsInventory.Data) == 0 {
			skills = chatwidget.EnabledSkillsForMentions(m.skillsInventory.Skills)
		}
	}
	return chatwidget.SubmissionMentionCatalog{Skills: skills, Plugins: append([]plugin.PluginSummary(nil), m.mentionPluginInventory...)}
}
