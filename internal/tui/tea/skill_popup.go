package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/internal/appserver"
	codextui "codex_go/internal/tui"
	chatwidget "codex_go/internal/tui/chatwidget"
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
	if !ok {
		m.skillPopup = skillPopupState{}
		return nil
	}
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
			response, err := reader(cwd)
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
	if m == nil || !m.skillPopup.Active {
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
	if insert == "$" {
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
	for _, existing := range m.composerMentionBindings {
		if existing == binding {
			return
		}
	}
	m.composerMentionBindings = append(m.composerMentionBindings, binding)
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
	if m == nil || !m.skillPopup.Active {
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
	out := make([]string, 0, len(m.composerMentionBindings))
	for _, binding := range m.composerMentionBindings {
		name := mentionBindingName(binding)
		if name == "" || mentions.Names[name] {
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
	if m == nil || m.skillsInventory == nil {
		return chatwidget.SubmissionMentionCatalog{}
	}
	cwd := strings.TrimSpace(m.sessionCWD)
	skills := chatwidget.EnabledSkillsForMentions(chatwidget.SkillsForCWD(cwd, m.skillsInventory))
	if len(skills) == 0 && len(m.skillsInventory.Data) == 0 {
		skills = chatwidget.EnabledSkillsForMentions(m.skillsInventory.Skills)
	}
	return chatwidget.SubmissionMentionCatalog{Skills: skills}
}
